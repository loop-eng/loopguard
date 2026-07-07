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
	StartedAt  time.Time `json:"started_at"`
}

type StatusResponse struct {
	Daemon   string        `json:"daemon"`
	Sessions []SessionInfo `json:"sessions"`
}

type DaemonBackend interface {
	GetSessions() []SessionInfo
	ResumeSession(ctx context.Context, id string) error
}
