package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

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
	logger *slog.Logger
	cfg    *config.Config
	cfgMu  sync.RWMutex
	cfgPath string
	ctx    context.Context
	cancel context.CancelFunc

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

func New(ctx context.Context, logger *slog.Logger, cfg *config.Config, cfgPath string) *Daemon {
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
		ContextFillPercent: cfg.SpinDetection.ContextFillPercent,
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
	if cfg.Sources.Gemini != "disabled" {
		discoverers = append(discoverers, discovery.NewGeminiDiscoverer(logger))
	}
	for _, customPath := range cfg.Sources.Custom {
		discoverers = append(discoverers, discovery.NewCustomDiscoverer(logger, []string{customPath}))
	}

	d := &Daemon{
		logger:      logger,
		cfg:         cfg,
		cfgPath:     cfgPath,
		ctx:         ctx,
		cancel:      cancel,
		registry:    discovery.NewRegistry(),
		discoverers: discoverers,
		watcher:     watcher.New(logger),
		analyzer:    analyzer.New(logger, budget, spinCfg, cfg.Pricing),
		enforcer:    enforcer.New(logger, cfg.Enforcement.SentinelFallback),
		notifier:    notify.New(logger, cfg.Notifications.Desktop, cfg.Notifications.Sound),
		history:     NewHistory(logger, filepath.Join(cfg.Traces.OutputDir, "history.jsonl")),
		ltfEmitter:  ltf.NewEmitter(logger, cfg.Traces.OutputDir, cfg.Traces.Enabled),
		parsers: map[string]parser.Parser{
			"claude": parser.NewClaudeParser(),
			"codex":  parser.NewCodexParser(),
			"gemini": parser.NewGeminiParser(),
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

	sockPath := api.SocketPath()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
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
	d.wg.Add(1)
	go func() { defer d.wg.Done(); d.watchConfig() }()
	d.wg.Add(1)
	go func() { defer d.wg.Done(); d.reapDeadPausedSessions() }()

	d.processEvents()

	d.logger.Info("daemon stopped")
	return nil
}

func (d *Daemon) Shutdown() {
	d.cancel()
	d.wg.Wait()
	d.analyzer.Stop()
	_ = d.watcher.Close()
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
			Terminated: s.Terminated,
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

	if session.Terminated {
		return fmt.Errorf("session %s terminated — process no longer exists", sessionID)
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

func (d *Daemon) GetSession(id string) (*api.SessionDetailResponse, error) {
	session, ok := d.registry.Get(id)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}

	cost := d.analyzer.SessionCost(id)

	d.pausedMu.RLock()
	paused := d.paused[id]
	d.pausedMu.RUnlock()

	return &api.SessionDetailResponse{
		SessionInfo: api.SessionInfo{
			ID:         session.ID,
			Agent:      session.Agent,
			ProjectDir: session.ProjectDir,
			Cost:       cost,
			Active:     session.Active,
			Paused:     paused,
			StartedAt:  session.StartedAt,
		},
		PID:       session.PID,
		LogPath:   session.Path,
		LastEvent: session.LastEvent,
	}, nil
}

func (d *Daemon) KillSession(ctx context.Context, sessionID string) error {
	session, ok := d.registry.Get(sessionID)
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	if err := d.enforcer.Execute(ctx, enforcer.ActionKill, session.PID, session.ProjectDir, "killed via API"); err != nil {
		return err
	}

	d.registry.Update(sessionID, func(s *discovery.Session) {
		s.Active = false
	})

	d.pausedMu.Lock()
	delete(d.paused, sessionID)
	d.pausedMu.Unlock()

	d.history.Record(sessionID, session.Agent, "killed", "api_request", d.analyzer.SessionCost(sessionID))
	d.ltfEmitter.EmitIntervention(sessionID, session.Agent, "killed", "api_request", d.analyzer.SessionCost(sessionID))

	return nil
}

func (d *Daemon) GetConfig() api.ConfigSnapshot {
	d.cfgMu.RLock()
	cfg := d.cfg
	d.cfgMu.RUnlock()

	return api.ConfigSnapshot{
		Budget: api.BudgetSnapshot{
			PerSessionUSD: cfg.Budget.PerSessionUSD,
			PerHourUSD:    cfg.Budget.PerHourUSD,
			PerDayUSD:     cfg.Budget.PerDayUSD,
			WarnAtPercent: cfg.Budget.WarnAtPercent,
		},
		SpinDetection: api.SpinDetectionSnapshot{
			RepeatedCalls:      cfg.SpinDetection.RepeatedCalls,
			ErrorEcho:          cfg.SpinDetection.ErrorEcho,
			StallMinutes:       cfg.SpinDetection.StallMinutes,
			CostVelocityPerMin: cfg.SpinDetection.CostVelocityPerMin,
			ContextFillPercent: cfg.SpinDetection.ContextFillPercent,
		},
		Enforcement: api.EnforcementSnapshot{
			Action:           cfg.Enforcement.Action,
			SentinelFallback: cfg.Enforcement.SentinelFallback,
		},
		Notifications: api.NotificationSnapshot{
			Desktop: cfg.Notifications.Desktop,
			Sound:   cfg.Notifications.Sound,
		},
		Sources: api.SourcesSnapshot{
			ClaudeCode: cfg.Sources.ClaudeCode,
			Codex:      cfg.Sources.Codex,
			Gemini:     cfg.Sources.Gemini,
			Custom:     cfg.Sources.Custom,
		},
		Traces: api.TracesSnapshot{
			Enabled:   cfg.Traces.Enabled,
			OutputDir: cfg.Traces.OutputDir,
		},
		Logging: api.LoggingSnapshot{
			Level: cfg.Logging.Level,
			File:  cfg.Logging.File,
		},
		ConfigPath: config.DefaultPath(),
	}
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

			p := d.parsers["claude"]
			if session, found := d.registry.Get(event.SessionID); found {
				if agentParser, ok := d.parsers[session.Agent]; ok {
					p = agentParser
				}
			} else {
				agent, projectDir := d.inferSessionInfo(event.Path)
				if d.registry.TryAdd(&discovery.Session{
					ID:         event.SessionID,
					Agent:      agent,
					Path:       event.Path,
					ProjectDir: projectDir,
					Active:     true,
					StartedAt:  time.Now(),
					LastEvent:  time.Now(),
				}) {
					if agentParser, ok := d.parsers[agent]; ok {
						p = agentParser
					}
					d.logger.Info("auto-registered session from watcher", "session", event.SessionID, "agent", agent)
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
		_ = d.notifier.Send(d.ctx, "LoopGuard Warning", alert.Message, notify.UrgencyNormal)

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
		_ = d.notifier.Send(d.ctx, "LoopGuard: Agent Paused", msg, notify.UrgencyCritical)

		d.history.Record(alert.SessionID, session.Agent, "paused", alert.Trigger, alert.Cost)
		d.ltfEmitter.EmitIntervention(alert.SessionID, session.Agent, "paused", alert.Trigger, alert.Cost)

	case analyzer.AlertKill:
		if err := d.enforcer.Execute(d.ctx, enforcer.ActionKill, session.PID, session.ProjectDir, alert.Message); err != nil {
			d.logger.Error("failed to kill session", "session", alert.SessionID, "error", err)
			return
		}
		d.registry.Update(alert.SessionID, func(s *discovery.Session) {
			s.Active = false
		})
		_ = d.notifier.Send(d.ctx, "LoopGuard: Agent Killed", alert.Message, notify.UrgencyCritical)
		d.history.Record(alert.SessionID, session.Agent, "killed", alert.Trigger, alert.Cost)
		d.ltfEmitter.EmitIntervention(alert.SessionID, session.Agent, "killed", alert.Trigger, alert.Cost)
		d.ltfEmitter.EmitSessionEnd(alert.SessionID, session.Agent, alert.Trigger, alert.Cost, session.StartedAt)
	}
}

func (d *Daemon) watchConfig() {
	path := d.cfgPath
	if path == "" {
		path = config.DefaultPath()
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		d.logger.Error("cannot watch config file", "error", err)
		return
	}
	defer func() {
		if err := w.Close(); err != nil {
			d.logger.Error("config watcher close error", "error", err)
		}
	}()

	// Watch the directory (not the file) so we catch editors that do
	// atomic write-rename (vim, sed -i, etc.).
	dir := filepath.Dir(path)
	if err := w.Add(dir); err != nil {
		d.logger.Error("cannot watch config directory", "error", err, "dir", dir)
		return
	}

	var debounce *time.Timer
	for {
		select {
		case <-d.ctx.Done():
			if debounce != nil {
				debounce.Stop()
			}
			return
		case event, ok := <-w.Events:
			if !ok {
				return
			}
			base := filepath.Base(event.Name)
			if base != filepath.Base(path) {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(500*time.Millisecond, func() {
				d.reloadConfig(path)
			})
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			d.logger.Error("config watcher error", "error", err)
		}
	}
}

func (d *Daemon) reloadConfig(path string) {
	newCfg, err := config.Load(path)
	if err != nil {
		d.logger.Error("config reload failed: parse error", "error", err)
		return
	}

	if err := config.Validate(newCfg); err != nil {
		d.logger.Error("config reload failed: validation error", "error", err)
		return
	}

	d.cfgMu.Lock()
	oldCfg := d.cfg
	d.cfg = newCfg
	d.cfgMu.Unlock()

	d.analyzer.UpdateBudget(
		newCfg.Budget.PerSessionUSD,
		newCfg.Budget.PerHourUSD,
		newCfg.Budget.PerDayUSD,
		newCfg.Budget.WarnAtPercent,
	)

	d.analyzer.UpdateSpinConfig(analyzer.SpinConfig{
		RepeatedCalls:      newCfg.SpinDetection.RepeatedCalls,
		ErrorEcho:          newCfg.SpinDetection.ErrorEcho,
		StallMinutes:       newCfg.SpinDetection.StallMinutes,
		CostVelocityPerMin: newCfg.SpinDetection.CostVelocityPerMin,
		ContextFillPercent: newCfg.SpinDetection.ContextFillPercent,
		WindowSize:         50,
	})

	d.notifier.UpdateSettings(newCfg.Notifications.Desktop, newCfg.Notifications.Sound)

	if len(newCfg.Pricing) > 0 {
		d.analyzer.UpdatePricing(newCfg.Pricing)
	}

	d.logger.Info("config reloaded",
		"budget_per_session", newCfg.Budget.PerSessionUSD,
		"budget_per_session_was", oldCfg.Budget.PerSessionUSD,
	)
}

func (d *Daemon) reapDeadPausedSessions() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			d.checkPausedSessions()
		}
	}
}

func (d *Daemon) checkPausedSessions() {
	d.pausedMu.RLock()
	pausedIDs := make([]string, 0, len(d.paused))
	for id := range d.paused {
		pausedIDs = append(pausedIDs, id)
	}
	d.pausedMu.RUnlock()

	for _, id := range pausedIDs {
		session, ok := d.registry.Get(id)
		if !ok {
			d.pausedMu.Lock()
			delete(d.paused, id)
			d.pausedMu.Unlock()
			continue
		}

		if session.PID > 0 && enforcer.ProcessAlive(session.PID) {
			continue
		}

		d.logger.Warn("paused session process exited",
			"session", id,
			"pid", session.PID,
			"agent", session.Agent,
		)

		d.pausedMu.Lock()
		delete(d.paused, id)
		d.pausedMu.Unlock()

		d.registry.Update(id, func(s *discovery.Session) {
			s.Active = false
			s.Terminated = true
		})

		if session.ProjectDir != "" {
			_ = enforcer.RemoveSentinel(session.ProjectDir)
		}

		cost := d.analyzer.SessionCost(id)
		d.history.Record(id, session.Agent, "terminated", "process_exited_while_paused", cost)
		d.ltfEmitter.EmitIntervention(id, session.Agent, "terminated", "process_exited_while_paused", cost)

		msg := fmt.Sprintf("Session %s (%s) — process exited while paused. PID %d no longer exists.",
			truncateID(id, 8), session.Agent, session.PID)
		_ = d.notifier.Send(d.ctx, "LoopGuard: Session Terminated", msg, notify.UrgencyNormal)
	}
}

func (d *Daemon) inferSessionInfo(path string) (agent, projectDir string) {
	storageDir := filepath.Dir(path)
	for _, disc := range d.discoverers {
		base := disc.BasePath()
		if base != "" && strings.HasPrefix(path, base) {
			agent = disc.Agent()
			if agent == "claude" {
				encoded := filepath.Base(storageDir)
				decoded := discovery.DecodeProjectDir(encoded)
				if info, err := os.Stat(decoded); err == nil && info.IsDir() {
					return agent, decoded
				}
			}
			return agent, storageDir
		}
	}
	return "claude", storageDir
}

func truncateID(id string, n int) string {
	if len(id) < n {
		return id
	}
	return id[:n]
}
