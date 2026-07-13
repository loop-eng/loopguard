package analyzer

import (
	"context"
	"log/slog"
	"sync"

	"github.com/loop-eng/loopguard/internal/parser"
)

type AlertLevel int

const (
	AlertWarn  AlertLevel = iota
	AlertPause
	AlertKill
)

func (l AlertLevel) String() string {
	switch l {
	case AlertWarn:
		return "warn"
	case AlertPause:
		return "pause"
	case AlertKill:
		return "kill"
	default:
		return "unknown"
	}
}

type Alert struct {
	SessionID string
	Level     AlertLevel
	Trigger   string
	Message   string
	Cost      float64
}

type Analyzer struct {
	logger   *slog.Logger
	alerts   chan Alert
	costCalc *CostCalculator
	budget   *BudgetEnforcer
	spinCfg  SpinConfig
	done     chan struct{}
	stopOnce sync.Once

	mu       sync.Mutex
	sessions map[string]*sessionState

	warned map[string]bool
}

type sessionState struct {
	spin *SpinDetector
	cost float64
}

func New(logger *slog.Logger, budget *BudgetEnforcer, spinCfg SpinConfig) *Analyzer {
	return &Analyzer{
		logger:   logger,
		alerts:   make(chan Alert, 64),
		costCalc: NewCostCalculator(logger),
		budget:   budget,
		spinCfg:  spinCfg,
		done:     make(chan struct{}),
		sessions: make(map[string]*sessionState),
		warned:   make(map[string]bool),
	}
}

func (a *Analyzer) Alerts() <-chan Alert {
	return a.alerts
}

func (a *Analyzer) SessionCost(sessionID string) float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.sessions[sessionID]; ok {
		return s.cost
	}
	return 0
}

func (a *Analyzer) RemoveSession(sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, sessionID)
	for key := range a.warned {
		if len(key) > len(sessionID) && key[:len(sessionID)+1] == sessionID+":" {
			delete(a.warned, key)
		}
	}
}

func (a *Analyzer) Process(ctx context.Context, sessionID string, event *parser.ParsedEvent) {
	a.mu.Lock()
	state, ok := a.sessions[sessionID]
	if !ok {
		state = &sessionState{
			spin: NewSpinDetector(a.spinCfg),
		}
		a.sessions[sessionID] = state
	}
	a.mu.Unlock()

	// Calculate cost
	if event.Tokens.Total() > 0 {
		cost := a.costCalc.Calculate(event.Tokens, event.Model)
		a.mu.Lock()
		state.cost += cost
		currentCost := state.cost
		a.mu.Unlock()

		// Check budget
		if br := a.budget.RecordCost(sessionID, cost); br != nil {
			msg := FormatBudgetAlert(br)
			if br.Exceeded {
				a.emit(Alert{
					SessionID: sessionID,
					Level:     AlertPause,
					Trigger:   "budget_exceeded",
					Message:   msg,
					Cost:      currentCost,
				})
			} else if br.Warning {
				warnKey := sessionID + ":" + br.Limit
				a.mu.Lock()
				alreadyWarned := a.warned[warnKey]
				if !alreadyWarned {
					a.warned[warnKey] = true
				}
				a.mu.Unlock()
				if !alreadyWarned {
					a.emit(Alert{
						SessionID: sessionID,
						Level:     AlertWarn,
						Trigger:   "budget_warning",
						Message:   msg,
						Cost:      currentCost,
					})
				}
			}
		}

		// Check spin detection
		spinResult := state.spin.Check(event, currentCost)
		if spinResult.IsSpinning {
			a.emit(Alert{
				SessionID: sessionID,
				Level:     AlertPause,
				Trigger:   "spin_detected",
				Message:   spinResult.Reasons[0],
				Cost:      currentCost,
			})
		}
	} else {
		// Still check spin for tool results (errors)
		a.mu.Lock()
		currentCost := state.cost
		a.mu.Unlock()
		spinResult := state.spin.Check(event, currentCost)
		if spinResult.IsSpinning {
			a.emit(Alert{
				SessionID: sessionID,
				Level:     AlertPause,
				Trigger:   "spin_detected",
				Message:   spinResult.Reasons[0],
				Cost:      currentCost,
			})
		}
	}
}

func (a *Analyzer) Stop() {
	a.stopOnce.Do(func() { close(a.done) })
}

func (a *Analyzer) emit(alert Alert) {
	a.logger.Warn("alert",
		"session", alert.SessionID,
		"level", alert.Level.String(),
		"trigger", alert.Trigger,
		"message", alert.Message,
		"cost", alert.Cost,
	)

	if alert.Level >= AlertPause {
		select {
		case a.alerts <- alert:
		case <-a.done:
			a.logger.Error("alert dropped during shutdown", "session", alert.SessionID, "trigger", alert.Trigger)
		}
		return
	}

	select {
	case a.alerts <- alert:
	default:
		a.logger.Warn("alert channel full, dropping warn alert", "session", alert.SessionID)
	}
}
