package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/loop-eng/loopguard/internal/analyzer"
	"github.com/loop-eng/loopguard/internal/api"
	"github.com/loop-eng/loopguard/internal/config"
	"github.com/loop-eng/loopguard/internal/discovery"
	"github.com/loop-eng/loopguard/internal/enforcer"
	"github.com/loop-eng/loopguard/internal/ltf"
	"github.com/loop-eng/loopguard/internal/notify"
	"github.com/loop-eng/loopguard/internal/parser"
	"github.com/loop-eng/loopguard/internal/watcher"
)

type Daemon struct {
	logger   *slog.Logger
	cfg      *config.Config
	ctx      context.Context
	cancel   context.CancelFunc

	registry    *discovery.Registry
	discoverers []discovery.Discoverer
	watcher     *watcher.Watcher
	analyzer    *analyzer.Analyzer
	enforcer    *enforcer.Enforcer
	notifier    *notify.Notifier
	apiServer   *api.Server
	history     *History
	ltfEmitter  *ltf.Emitter

	parsers map[string]parser.Parser // agent name → parser

	pausedMu sync.RWMutex
	paused   map[string]bool

	wg sync.WaitGroup
}

func New(ctx context.Context, logger *slog.Logger, cfg *config.Config) *Daemon {
	ctx, cancel := context.WithCancel(ctx)

	budget := analyzer.NewBudgetEnforcer(
		cfg.Budget.PerSessionUSD,
		cfg.Budget.PerHourUSD,
		cfg.Budget.PerDayUSD,
		cfg.Budget.WarnAtPercent,
	)

	spinCfg := analyzer.SpinConfig{
		RepeatedCalls:      cfg.SpinDetection.RepeatedCalls,
		ErrorEcho:          cfg.SpinDetection.ErrorEcho,
		StallMinutes:       cfg.SpinDetection.StallMinutes,
		CostVelocityPerMin: cfg.SpinDetection.CostVelocityPerMin,
		WindowSize:         50,
	}

	// Build discoverers based on config
	var discoverers []discovery.Discoverer
	if cfg.Sources.ClaudeCode != "disabled" {
		discoverers = append(discoverers, discovery.NewClaudeDiscoverer(logger))
	}
	if cfg.Sources.Codex != "disabled" {
		discoverers = append(discoverers, discovery.NewCodexDiscoverer(logger))
	}
	if len(cfg.Sources.Custom) > 0 {
		discoverers = append(discoverers, discovery.NewCustomDiscoverer(logger, cfg.Sources.Custom))
	}

	d := &Daemon{
		logger:      logger,
		cfg:         cfg,
		ctx:         ctx,
		cancel:      cancel,
		registry:    discovery.NewRegistry(),
		discoverers: discoverers,
		watcher:     watcher.New(logger),
		analyzer:    analyzer.New(logger, budget, spinCfg),
		enforcer:    enforcer.New(logger, cfg.Enforcement.SentinelFallback),
		notifier:    notify.New(logger, cfg.Notifications.Desktop, cfg.Notifications.Sound),
		history:     NewHistory(logger, filepath.Join(cfg.Traces.OutputDir, "history.jsonl")),
		ltfEmitter:  ltf.NewEmitter(logger, cfg.Traces.OutputDir, cfg.Traces.Enabled),
		parsers: map[string]parser.Parser{
			"claude": parser.NewClaudeParser(),
			"codex":  parser.NewCodexParser(),
		},
		paused: make(map[string]bool),
	}

	d.apiServer = api.NewWithDaemon(logger, d)
	return d
}

func (d *Daemon) Run() error {
	d.logger.Info("daemon starting",
		"discoverers", len(d.discoverers),
		"budget_per_session", d.cfg.Budget.PerSessionUSD,
	)

	d.discoverSessions()

	sockPath := d.socketPath()
	go func() {
		if err := d.apiServer.Serve(d.ctx, sockPath); err != nil {
			d.logger.Error("API server error", "error", err)
		}
	}()

	for _, disc := range d.discoverers {
		base := disc.BasePath()
		if base == "" {
			continue
		}
		d.wg.Add(1)
		go func(basePath string) {
			defer d.wg.Done()
			if err := d.watcher.Watch(d.ctx, basePath); err != nil && err != context.Canceled {
				d.logger.Error("watcher error", "error", err, "path", basePath)
			}
		}(base)
	}

	d.wg.Add(1)
	go func() { defer d.wg.Done(); d.handleAlerts() }()
	d.wg.Add(1)
	go func() { defer d.wg.Done(); d.rediscoveryLoop() }()

	d.processEvents()

	d.logger.Info("daemon stopped")
	return nil
}

func (d *Daemon) Shutdown() {
	d.analyzer.Stop()
	d.cancel()
	d.wg.Wait()
	d.watcher.Close()
	d.ltfEmitter.Close()
}

func (d *Daemon) GetSessions() []api.SessionInfo {
	sessions := d.registry.All()

	d.pausedMu.RLock()
	pausedCopy := make(map[string]bool, len(d.paused))
	for k, v := range d.paused {
		pausedCopy[k] = v
	}
	d.pausedMu.RUnlock()

	infos := make([]api.SessionInfo, len(sessions))
	for i, s := range sessions {
		cost := d.analyzer.SessionCost(s.ID)
		infos[i] = api.SessionInfo{
			ID:         s.ID,
			Agent:      s.Agent,
			ProjectDir: s.ProjectDir,
			Cost:       cost,
			Active:     s.Active,
			Paused:     pausedCopy[s.ID],
			StartedAt:  s.StartedAt,
		}
	}
	return infos
}

func (d *Daemon) ResumeSession(ctx context.Context, sessionID string) error {
	session, ok := d.registry.Get(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	d.pausedMu.RLock()
	isPaused := d.paused[sessionID]
	d.pausedMu.RUnlock()
	if !isPaused {
		return fmt.Errorf("session not paused: %s", sessionID)
	}

	if err := d.enforcer.Resume(ctx, session.PID, session.ProjectDir); err != nil {
		return err
	}

	d.pausedMu.Lock()
	delete(d.paused, sessionID)
	d.pausedMu.Unlock()

	d.registry.Update(sessionID, func(s *discovery.Session) {
		s.Active = true
	})

	return nil
}

func (d *Daemon) discoverSessions() {
	total := 0
	for _, disc := range d.discoverers {
		sessions := disc.Discover(24 * time.Hour)
		for _, s := range sessions {
			d.registry.Add(s)
			d.watcher.AddFile(s.Path, s.ID, true)
		}
		total += len(sessions)
	}
	d.logger.Info("discovered sessions", "count", total)
}

func (d *Daemon) rediscoveryLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.discoverSessions()
		}
	}
}

func (d *Daemon) processEvents() {
	for {
		select {
		case <-d.ctx.Done():
			return
		case event, ok := <-d.watcher.Events():
			if !ok {
				return
			}

			var p parser.Parser = d.parsers["claude"]
			if session, found := d.registry.Get(event.SessionID); found {
				if agentParser, ok := d.parsers[session.Agent]; ok {
					p = agentParser
				}
			}

			for _, line := range event.Lines {
				parsed, err := p.Parse(line)
				if err != nil {
					d.logger.Debug("parse error", "error", err, "session", event.SessionID)
					continue
				}

				for _, ev := range parsed {
					ev.SessionID = event.SessionID
					d.analyzer.Process(d.ctx, event.SessionID, ev)
				}
			}
		}
	}
}

func (d *Daemon) handleAlerts() {
	for {
		select {
		case <-d.ctx.Done():
			return
		case alert, ok := <-d.analyzer.Alerts():
			if !ok {
				return
			}
			d.executeAlert(alert)
		}
	}
}

func (d *Daemon) executeAlert(alert analyzer.Alert) {
	session, ok := d.registry.Get(alert.SessionID)
	if !ok {
		d.logger.Warn("alert for unknown session", "session", alert.SessionID)
		return
	}

	switch alert.Level {
	case analyzer.AlertWarn:
		d.notifier.Send(d.ctx, "LoopGuard Warning", alert.Message, notify.UrgencyNormal)

	case analyzer.AlertPause:
		d.pausedMu.RLock()
		alreadyPaused := d.paused[alert.SessionID]
		d.pausedMu.RUnlock()
		if alreadyPaused {
			return
		}

		err := d.enforcer.Execute(d.ctx, enforcer.ActionPause, session.PID, session.ProjectDir, alert.Message)
		if err != nil {
			d.logger.Error("failed to pause session", "session", alert.SessionID, "error", err)
			return
		}

		d.pausedMu.Lock()
		d.paused[alert.SessionID] = true
		d.pausedMu.Unlock()

		d.registry.Update(alert.SessionID, func(s *discovery.Session) {
			s.Active = false
		})

		msg := fmt.Sprintf("%s\nCost: $%.2f\nResume: loopguard resume %s",
			alert.Message, alert.Cost, truncateID(alert.SessionID, 8))
		d.notifier.Send(d.ctx, "LoopGuard: Agent Paused", msg, notify.UrgencyCritical)

		d.history.Record(alert.SessionID, session.Agent, "paused", alert.Trigger, alert.Cost)
		d.ltfEmitter.EmitIntervention(alert.SessionID, session.Agent, "paused", alert.Trigger, alert.Cost)

	case analyzer.AlertKill:
		d.enforcer.Execute(d.ctx, enforcer.ActionKill, session.PID, session.ProjectDir, alert.Message)
		d.registry.Update(alert.SessionID, func(s *discovery.Session) {
			s.Active = false
		})
		d.notifier.Send(d.ctx, "LoopGuard: Agent Killed", alert.Message, notify.UrgencyCritical)
		d.history.Record(alert.SessionID, session.Agent, "killed", alert.Trigger, alert.Cost)
		d.ltfEmitter.EmitIntervention(alert.SessionID, session.Agent, "killed", alert.Trigger, alert.Cost)
		d.ltfEmitter.EmitSessionEnd(alert.SessionID, session.Agent, alert.Trigger, alert.Cost, session.StartedAt)
	}
}

func (d *Daemon) socketPath() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "loopguard")
	os.MkdirAll(dir, 0700)
	return filepath.Join(dir, "loopguard.sock")
}

func truncateID(id string, n int) string {
	if len(id) < n {
		return id
	}
	return id[:n]
}
