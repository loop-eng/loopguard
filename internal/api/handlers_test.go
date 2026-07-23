package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mockBackend implements DaemonBackend for handler testing.
type mockBackend struct {
	sessions     []SessionInfo
	sessionByID  map[string]*SessionDetailResponse
	killErr      map[string]error
	configSnap   ConfigSnapshot
}

func (m *mockBackend) GetSessions() []SessionInfo { return m.sessions }

func (m *mockBackend) GetSession(id string) (*SessionDetailResponse, error) {
	if d, ok := m.sessionByID[id]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("session not found: %s", id)
}

func (m *mockBackend) ResumeSession(_ context.Context, id string) error {
	return nil
}

func (m *mockBackend) KillSession(_ context.Context, id string) error {
	if err, ok := m.killErr[id]; ok {
		return err
	}
	if _, ok := m.sessionByID[id]; !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	return nil
}

func (m *mockBackend) GetConfig() ConfigSnapshot {
	return m.configSnap
}

func TestHandleSessionDetailFound(t *testing.T) {
	now := time.Now()
	backend := &mockBackend{
		sessionByID: map[string]*SessionDetailResponse{
			"sess-1": {
				SessionInfo: SessionInfo{
					ID:    "sess-1",
					Agent: "claude",
					Cost:  1.23,
				},
				PID:       12345,
				LogPath:   "/tmp/session.jsonl",
				LastEvent: now,
			},
		},
	}

	srv := NewWithDaemon(slog.Default(), backend)
	req := httptest.NewRequest("GET", "/api/sessions/sess-1", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp SessionDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID != "sess-1" {
		t.Errorf("ID = %q, want sess-1", resp.ID)
	}
	if resp.PID != 12345 {
		t.Errorf("PID = %d, want 12345", resp.PID)
	}
	if resp.Cost != 1.23 {
		t.Errorf("Cost = %f, want 1.23", resp.Cost)
	}
}

func TestHandleSessionDetailNotFound(t *testing.T) {
	backend := &mockBackend{
		sessionByID: map[string]*SessionDetailResponse{},
	}

	srv := NewWithDaemon(slog.Default(), backend)
	req := httptest.NewRequest("GET", "/api/sessions/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}

	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] == "" {
		t.Error("expected error message in response body")
	}
}

func TestHandleKillSuccess(t *testing.T) {
	backend := &mockBackend{
		sessionByID: map[string]*SessionDetailResponse{
			"sess-1": {SessionInfo: SessionInfo{ID: "sess-1"}},
		},
		killErr: map[string]error{},
	}

	srv := NewWithDaemon(slog.Default(), backend)
	req := httptest.NewRequest("POST", "/api/sessions/sess-1/kill", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "killed" {
		t.Errorf("status = %q, want killed", resp["status"])
	}
	if resp["session"] != "sess-1" {
		t.Errorf("session = %q, want sess-1", resp["session"])
	}
}

func TestHandleKillNotFound(t *testing.T) {
	backend := &mockBackend{
		sessionByID: map[string]*SessionDetailResponse{},
		killErr:     map[string]error{},
	}

	srv := NewWithDaemon(slog.Default(), backend)
	req := httptest.NewRequest("POST", "/api/sessions/ghost/kill", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleConfig(t *testing.T) {
	backend := &mockBackend{
		configSnap: ConfigSnapshot{
			Budget: BudgetSnapshot{
				PerSessionUSD: 20.0,
				PerHourUSD:    50.0,
				PerDayUSD:     200.0,
				WarnAtPercent: 80,
			},
			SpinDetection: SpinDetectionSnapshot{
				RepeatedCalls:      3,
				ErrorEcho:          3,
				StallMinutes:       10,
				CostVelocityPerMin: 2.0,
				ContextFillPercent: 85,
			},
			Enforcement: EnforcementSnapshot{
				Action:           "pause",
				SentinelFallback: true,
			},
			Notifications: NotificationSnapshot{
				Desktop: true,
				Sound:   true,
			},
			Sources: SourcesSnapshot{
				ClaudeCode: "auto",
				Codex:      "auto",
				Gemini:     "auto",
			},
			ConfigPath: "/home/user/.config/loopguard/config.yaml",
		},
	}

	srv := NewWithDaemon(slog.Default(), backend)
	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp ConfigSnapshot
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Budget.PerSessionUSD != 20.0 {
		t.Errorf("Budget.PerSessionUSD = %v, want 20.0", resp.Budget.PerSessionUSD)
	}
	if resp.SpinDetection.ContextFillPercent != 85 {
		t.Errorf("SpinDetection.ContextFillPercent = %d, want 85", resp.SpinDetection.ContextFillPercent)
	}
	if resp.Enforcement.Action != "pause" {
		t.Errorf("Enforcement.Action = %q, want pause", resp.Enforcement.Action)
	}
	if resp.Sources.Gemini != "auto" {
		t.Errorf("Sources.Gemini = %q, want auto", resp.Sources.Gemini)
	}
	if resp.ConfigPath == "" {
		t.Error("expected ConfigPath to be non-empty")
	}
}

func TestHandleConfigNoBackend(t *testing.T) {
	srv := New(slog.Default())
	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}
