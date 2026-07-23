package api

import (
	"context"
	"time"
)

type SessionInfo struct {
	ID         string    `json:"id"`
	Agent      string    `json:"agent"`
	ProjectDir string    `json:"project_dir"`
	Cost       float64   `json:"cost"`
	Active     bool      `json:"active"`
	Paused     bool      `json:"paused"`
	Terminated bool      `json:"terminated"`
	StartedAt  time.Time `json:"started_at"`
}

// SessionDetailResponse wraps a single session with extra fields
// not present in the list view.
type SessionDetailResponse struct {
	SessionInfo
	PID       int       `json:"pid"`
	LogPath   string    `json:"log_path"`
	Alerts    int       `json:"alert_count"`
	LastEvent time.Time `json:"last_event"`
}

type StatusResponse struct {
	Daemon   string        `json:"daemon"`
	Sessions []SessionInfo `json:"sessions"`
}

// ConfigSnapshot is a JSON-safe representation of the running config.
type ConfigSnapshot struct {
	Budget        BudgetSnapshot        `json:"budget"`
	SpinDetection SpinDetectionSnapshot `json:"spin_detection"`
	Enforcement   EnforcementSnapshot   `json:"enforcement"`
	Notifications NotificationSnapshot  `json:"notifications"`
	Sources       SourcesSnapshot       `json:"sources"`
	Traces        TracesSnapshot        `json:"traces"`
	Logging       LoggingSnapshot       `json:"logging"`
	ConfigPath    string                `json:"config_path"`
}

type BudgetSnapshot struct {
	PerSessionUSD float64 `json:"per_session_usd"`
	PerHourUSD    float64 `json:"per_hour_usd"`
	PerDayUSD     float64 `json:"per_day_usd"`
	WarnAtPercent int     `json:"warn_at_percent"`
}

type SpinDetectionSnapshot struct {
	RepeatedCalls      int     `json:"repeated_calls"`
	ErrorEcho          int     `json:"error_echo"`
	StallMinutes       int     `json:"stall_minutes"`
	CostVelocityPerMin float64 `json:"cost_velocity_per_min"`
	ContextFillPercent int     `json:"context_fill_percent"`
}

type EnforcementSnapshot struct {
	Action           string `json:"action"`
	SentinelFallback bool   `json:"sentinel_fallback"`
}

type NotificationSnapshot struct {
	Desktop bool `json:"desktop"`
	Sound   bool `json:"sound"`
}

type SourcesSnapshot struct {
	ClaudeCode string   `json:"claude_code"`
	Codex      string   `json:"codex"`
	Gemini     string   `json:"gemini"`
	Custom     []string `json:"custom"`
}

type TracesSnapshot struct {
	Enabled   bool   `json:"enabled"`
	OutputDir string `json:"output_dir"`
}

type LoggingSnapshot struct {
	Level string `json:"level"`
	File  string `json:"file"`
}

type DaemonBackend interface {
	GetSessions() []SessionInfo
	GetSession(id string) (*SessionDetailResponse, error)
	ResumeSession(ctx context.Context, id string) error
	KillSession(ctx context.Context, id string) error
	GetConfig() ConfigSnapshot
}
