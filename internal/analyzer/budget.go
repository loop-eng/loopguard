package analyzer

import (
	"fmt"
	"sync"
	"time"
)

type BudgetResult struct {
	Exceeded   bool
	Warning    bool
	Limit      string  // "per_session", "per_hour", "per_day"
	Current    float64
	Maximum    float64
	Percentage float64
}

type BudgetEnforcer struct {
	mu sync.Mutex

	perSession float64
	perHour    float64
	perDay     float64
	warnPct    float64

	sessionCosts map[string]float64
	hourlyCosts  []timedCost
	dailyCost    float64
	dayStart     time.Time
}

func NewBudgetEnforcer(perSession, perHour, perDay float64, warnPct int) *BudgetEnforcer {
	return &BudgetEnforcer{
		perSession:   perSession,
		perHour:      perHour,
		perDay:       perDay,
		warnPct:      float64(warnPct) / 100.0,
		sessionCosts: make(map[string]float64),
		dayStart:     startOfDay(time.Now()),
	}
}

func (be *BudgetEnforcer) RecordCost(sessionID string, cost float64) *BudgetResult {
	be.mu.Lock()
	defer be.mu.Unlock()

	now := time.Now()

	// Reset daily counter at midnight
	today := startOfDay(now)
	if today.After(be.dayStart) {
		be.dailyCost = 0
		be.dayStart = today
	}

	be.sessionCosts[sessionID] += cost
	be.dailyCost += cost
	be.hourlyCosts = append(be.hourlyCosts, timedCost{timestamp: now, cost: cost})

	// Trim hourly window
	cutoff := now.Add(-1 * time.Hour)
	trimIdx := 0
	for trimIdx < len(be.hourlyCosts) && be.hourlyCosts[trimIdx].timestamp.Before(cutoff) {
		trimIdx++
	}
	if trimIdx > 0 {
		be.hourlyCosts = be.hourlyCosts[trimIdx:]
	}

	// Check per-session
	sessionTotal := be.sessionCosts[sessionID]
	if r := be.check("per_session", sessionTotal, be.perSession); r != nil {
		return r
	}

	// Check per-hour
	var hourTotal float64
	for _, tc := range be.hourlyCosts {
		hourTotal += tc.cost
	}
	if r := be.check("per_hour", hourTotal, be.perHour); r != nil {
		return r
	}

	// Check per-day
	if r := be.check("per_day", be.dailyCost, be.perDay); r != nil {
		return r
	}

	return nil
}

func (be *BudgetEnforcer) SessionCost(sessionID string) float64 {
	be.mu.Lock()
	defer be.mu.Unlock()
	return be.sessionCosts[sessionID]
}

func (be *BudgetEnforcer) DailyCost() float64 {
	be.mu.Lock()
	defer be.mu.Unlock()
	return be.dailyCost
}

func (be *BudgetEnforcer) check(limit string, current, maximum float64) *BudgetResult {
	if maximum <= 0 {
		return nil
	}
	pct := current / maximum

	if pct >= 1.0 {
		return &BudgetResult{
			Exceeded:   true,
			Limit:      limit,
			Current:    current,
			Maximum:    maximum,
			Percentage: pct * 100,
		}
	}

	if pct >= be.warnPct {
		return &BudgetResult{
			Warning:    true,
			Limit:      limit,
			Current:    current,
			Maximum:    maximum,
			Percentage: pct * 100,
		}
	}

	return nil
}

func FormatBudgetAlert(r *BudgetResult) string {
	if r.Exceeded {
		return fmt.Sprintf("Budget exceeded: %s $%.2f/$%.2f (%.0f%%)",
			r.Limit, r.Current, r.Maximum, r.Percentage)
	}
	return fmt.Sprintf("Budget warning: %s $%.2f/$%.2f (%.0f%%)",
		r.Limit, r.Current, r.Maximum, r.Percentage)
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
