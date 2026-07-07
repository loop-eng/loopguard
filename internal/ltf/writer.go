package ltf

import (
	"encoding/json"
	"io"
	"time"
)

type Event struct {
	LTFVersion string         `json:"ltf_version"`
	LoopID     string         `json:"loop_id"`
	SessionID  string         `json:"session_id,omitempty"`
	Iteration  int            `json:"iteration,omitempty"`
	Timestamp  time.Time      `json:"timestamp"`
	Phase      string         `json:"phase"`
	Agent      *AgentInfo     `json:"agent,omitempty"`
	Action     *EventAction   `json:"action,omitempty"`
	Tokens     *TokenInfo     `json:"tokens,omitempty"`
	CostUSD    float64        `json:"cost_usd,omitempty"`
	DurationMs int64          `json:"duration_ms,omitempty"`
	Result     *EventResult   `json:"result,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type LoopSummary struct {
	LTFVersion        string         `json:"ltf_version"`
	Type              string         `json:"type"`
	LoopID            string         `json:"loop_id"`
	SessionID         string         `json:"session_id,omitempty"`
	StartedAt         time.Time      `json:"started_at"`
	EndedAt           time.Time      `json:"ended_at"`
	TotalCostUSD      float64        `json:"total_cost_usd"`
	TotalDurationMs   int64          `json:"total_duration_ms"`
	TerminationReason string         `json:"termination_reason"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}

type AgentInfo struct {
	Name     string `json:"name,omitempty"`
	Role     string `json:"role,omitempty"`
	Provider string `json:"provider,omitempty"`
}

type EventAction struct {
	Type   string `json:"type"`
	Detail string `json:"detail,omitempty"`
}

type TokenInfo struct {
	Input      int `json:"input,omitempty"`
	Output     int `json:"output,omitempty"`
	Cached     int `json:"cached,omitempty"`
	CacheWrite int `json:"cache_write,omitempty"`
}

type EventResult struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type Writer struct {
	w   io.Writer
	enc *json.Encoder
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{
		w:   w,
		enc: json.NewEncoder(w),
	}
}

func (w *Writer) WriteEvent(event Event) error {
	if event.LTFVersion == "" {
		event.LTFVersion = "1.0"
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	return w.enc.Encode(event)
}

func (w *Writer) WriteSummary(summary LoopSummary) error {
	if summary.LTFVersion == "" {
		summary.LTFVersion = "1.0"
	}
	if summary.Type == "" {
		summary.Type = "loop_summary"
	}
	return w.enc.Encode(summary)
}
