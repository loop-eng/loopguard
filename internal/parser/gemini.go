package parser

import (
	"encoding/json"
	"strings"
	"time"
)

// GeminiParser parses Gemini CLI session logs. Gemini CLI writes sessions
// in two co-existing formats:
//
//   - Legacy: session-*.json — a single monolithic JSON object holding the
//     full conversation, rewritten in full on every turn. The watcher
//     delivers the entire file content as one "line" on each rewrite (see
//     internal/watcher.Tailer's whole-file mode for .json paths).
//   - Current: session-*.jsonl — append-only JSONL, one record per line.
//
// Parse auto-detects which of the two shapes a given chunk is: a JSONL
// record carries a non-empty "type" field, while the legacy conversation
// object carries a top-level "messages" array and no "type" field.
//
// NOTE: The exact JSONL schema below reflects Gemini CLI's session log
// format as documented at the time this parser was written. Some fields
// (per-line timestamp, per-line model name) are not present in every known
// sample and are handled defensively with graceful fallbacks (current time,
// FallbackContextWindow) rather than assumed to always be populated. If a
// future Gemini CLI release changes the schema, update the structs below.
type GeminiParser struct {
	// legacySeen tracks how many messages of a legacy conversation's
	// Messages array have already been emitted, keyed by sessionId. Since
	// the legacy format rewrites the whole file on every turn, each parse
	// call receives the full (growing) message list; only the tail beyond
	// what's already been seen is emitted, avoiding duplicate token counts.
	legacySeen map[string]int

	// seenUpdates dedups message_update lines (JSONL) by "id" so a
	// duplicated or replayed line doesn't double-count tokens.
	seenUpdates      map[string]bool
	seenUpdatesCount int

	// lastModel is the most recently observed model name/version, used to
	// stamp events (like message_update) that don't carry their own model
	// field so downstream cost/context-window lookups have something to
	// work with.
	lastModel string
}

func NewGeminiParser() *GeminiParser {
	return &GeminiParser{
		legacySeen:  make(map[string]int),
		seenUpdates: make(map[string]bool, 256),
	}
}

// --- JSONL (current) format types ---

type geminiJSONLEntry struct {
	Type      string        `json:"type"`
	ID        string        `json:"id"`
	SessionID string        `json:"sessionId"`
	Content   []geminiPart  `json:"content"`
	Tokens    *geminiTokens `json:"tokens"`
	StartTime string        `json:"startTime"`

	// Defensive/forward-compatible fields not guaranteed present in every
	// Gemini CLI version's JSONL output.
	Timestamp    string `json:"timestamp"`
	Model        string `json:"model"`
	ModelVersion string `json:"modelVersion"`
}

type geminiTokens struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

type geminiPart struct {
	Text             string        `json:"text,omitempty"`
	FunctionCall     *geminiFnCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFnResp `json:"functionResponse,omitempty"`
}

type geminiFnCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type geminiFnResp struct {
	Name     string `json:"name"`
	Response any    `json:"response"`
}

// --- Legacy monolithic JSON format types ---

type geminiConversation struct {
	SessionID string          `json:"sessionId"`
	Messages  []geminiMessage `json:"messages"`
}

type geminiMessage struct {
	Role          string       `json:"role"` // "user" or "model"
	Parts         []geminiPart `json:"parts"`
	UsageMetadata *geminiUsage `json:"usageMetadata,omitempty"`
	ModelVersion  string       `json:"modelVersion,omitempty"`
	Timestamp     string       `json:"timestamp,omitempty"` // defensive, not in every sample
}

type geminiUsage struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
}

// geminiProbe cheaply distinguishes JSONL vs legacy format without fully
// unmarshaling into either concrete type first.
type geminiProbe struct {
	Type     string          `json:"type"`
	Messages json.RawMessage `json:"messages"`
}

func (p *GeminiParser) Parse(line []byte) ([]*ParsedEvent, error) {
	var probe geminiProbe
	if err := json.Unmarshal(line, &probe); err != nil {
		return nil, err
	}

	if probe.Type != "" {
		return p.parseJSONLEntry(line)
	}
	if len(probe.Messages) > 0 && string(probe.Messages) != "null" {
		return p.parseLegacyConversation(line)
	}
	// Valid JSON but neither recognized shape (e.g. an empty object, or a
	// session-metadata-only legacy shell with no messages array yet).
	return nil, nil
}

func (p *GeminiParser) parseJSONLEntry(line []byte) ([]*ParsedEvent, error) {
	var entry geminiJSONLEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, err
	}

	ts := geminiTimestamp(entry.Timestamp, entry.StartTime)
	if m := firstNonEmpty(entry.ModelVersion, entry.Model); m != "" {
		p.lastModel = m
	}

	switch entry.Type {
	case "session_metadata":
		return nil, nil

	case "user":
		var events []*ParsedEvent
		for _, part := range entry.Content {
			if part.FunctionResponse == nil {
				continue
			}
			isErr, errMsg := geminiResponseError(part.FunctionResponse.Response)
			events = append(events, &ParsedEvent{
				SessionID:   entry.SessionID,
				UUID:        entry.ID,
				Timestamp:   ts,
				EntryType:   "user",
				ContentType: ContentToolResult,
				ToolResult:  contentToString(part.FunctionResponse.Response),
				IsError:     isErr,
				ErrorMsg:    errMsg,
			})
		}
		return events, nil

	case "gemini":
		var events []*ParsedEvent
		for _, part := range entry.Content {
			switch {
			case part.FunctionCall != nil:
				ev := &ParsedEvent{
					SessionID:   entry.SessionID,
					UUID:        entry.ID,
					Timestamp:   ts,
					EntryType:   "assistant",
					Model:       p.lastModel,
					ContentType: ContentToolUse,
					ToolName:    part.FunctionCall.Name,
				}
				if len(part.FunctionCall.Args) > 0 {
					ev.ToolInput = string(part.FunctionCall.Args)
					// NOTE: extractFilesChanged only recognizes Claude Code's
					// tool naming convention (Edit/Write/NotebookEdit). Gemini
					// CLI's function-call names for file edits are not
					// independently verified here, so FilesChanged (and thus
					// the no-progress-stall heuristic) may not populate for
					// Gemini sessions until Gemini's actual tool schema is
					// confirmed against live session data.
					ev.FilesChanged = extractFilesChanged(part.FunctionCall.Name, parseInputMap(string(part.FunctionCall.Args)))
				}
				events = append(events, ev)
			case part.Text != "":
				events = append(events, &ParsedEvent{
					SessionID:   entry.SessionID,
					UUID:        entry.ID,
					Timestamp:   ts,
					EntryType:   "assistant",
					Model:       p.lastModel,
					ContentType: ContentText,
				})
			}
		}
		return events, nil

	case "message_update":
		if entry.Tokens == nil {
			return nil, nil
		}
		if entry.ID != "" {
			if p.seenUpdates[entry.ID] {
				return nil, nil
			}
			p.seenUpdates[entry.ID] = true
			p.seenUpdatesCount++
			if p.seenUpdatesCount > maxSeenRequests {
				p.seenUpdates = make(map[string]bool, 256)
				p.seenUpdatesCount = 0
			}
		}
		return []*ParsedEvent{{
			SessionID:   entry.SessionID,
			UUID:        entry.ID,
			Timestamp:   ts,
			EntryType:   "assistant",
			Model:       p.lastModel,
			ContentType: ContentText,
			Tokens: TokenUsage{
				InputTokens:  entry.Tokens.Input,
				OutputTokens: entry.Tokens.Output,
			},
		}}, nil

	default:
		// Unknown/future entry type — skip gracefully rather than error.
		return nil, nil
	}
}

func (p *GeminiParser) parseLegacyConversation(line []byte) ([]*ParsedEvent, error) {
	var conv geminiConversation
	if err := json.Unmarshal(line, &conv); err != nil {
		return nil, err
	}

	seenCount := p.legacySeen[conv.SessionID]
	if seenCount > len(conv.Messages) {
		// The conversation shrank (new session reusing the same ID, or a
		// truncated rewrite) — reprocess from the start rather than skip
		// everything.
		seenCount = 0
	}

	var events []*ParsedEvent
	for i := seenCount; i < len(conv.Messages); i++ {
		events = append(events, p.parseLegacyMessage(conv.SessionID, &conv.Messages[i])...)
	}

	p.legacySeen[conv.SessionID] = len(conv.Messages)
	return events, nil
}

func (p *GeminiParser) parseLegacyMessage(sessionID string, msg *geminiMessage) []*ParsedEvent {
	ts := geminiTimestamp(msg.Timestamp)
	if msg.ModelVersion != "" {
		p.lastModel = msg.ModelVersion
	}

	var tokens TokenUsage
	if msg.UsageMetadata != nil {
		tokens = TokenUsage{
			InputTokens:     msg.UsageMetadata.PromptTokenCount,
			OutputTokens:    msg.UsageMetadata.CandidatesTokenCount + msg.UsageMetadata.ThoughtsTokenCount,
			CacheReadTokens: msg.UsageMetadata.CachedContentTokenCount,
		}
	}

	entryType := "user"
	if msg.Role == "model" {
		entryType = "assistant"
	}

	var events []*ParsedEvent
	emittedTokens := false

	for _, part := range msg.Parts {
		switch {
		case part.FunctionCall != nil:
			ev := &ParsedEvent{
				SessionID:   sessionID,
				Timestamp:   ts,
				EntryType:   entryType,
				Model:       p.lastModel,
				ContentType: ContentToolUse,
				ToolName:    part.FunctionCall.Name,
			}
			if len(part.FunctionCall.Args) > 0 {
				ev.ToolInput = string(part.FunctionCall.Args)
				ev.FilesChanged = extractFilesChanged(part.FunctionCall.Name, parseInputMap(string(part.FunctionCall.Args)))
			}
			if !emittedTokens {
				ev.Tokens = tokens
				emittedTokens = true
			}
			events = append(events, ev)

		case part.FunctionResponse != nil:
			isErr, errMsg := geminiResponseError(part.FunctionResponse.Response)
			events = append(events, &ParsedEvent{
				SessionID:   sessionID,
				Timestamp:   ts,
				EntryType:   entryType,
				ContentType: ContentToolResult,
				ToolResult:  contentToString(part.FunctionResponse.Response),
				IsError:     isErr,
				ErrorMsg:    errMsg,
			})

		case part.Text != "":
			ev := &ParsedEvent{
				SessionID:   sessionID,
				Timestamp:   ts,
				EntryType:   entryType,
				Model:       p.lastModel,
				ContentType: ContentText,
			}
			if !emittedTokens {
				ev.Tokens = tokens
				emittedTokens = true
			}
			events = append(events, ev)
		}
	}

	// If the message carried usage but produced no part-based event (e.g.
	// empty/unparseable parts), still emit a token-only event so cost isn't
	// silently dropped.
	if !emittedTokens && tokens.Total() > 0 {
		events = append(events, &ParsedEvent{
			SessionID:   sessionID,
			Timestamp:   ts,
			EntryType:   entryType,
			Model:       p.lastModel,
			ContentType: ContentText,
			Tokens:      tokens,
		})
	}

	return events
}

// geminiResponseError applies a best-effort heuristic to determine whether
// a functionResponse payload represents an error. Gemini's function
// response schema for error cases is not fully standardized across tools,
// so this checks common conventions: an "error" key, an "isError"/"is_error"
// boolean, or the substring "error" in a plain string response.
func geminiResponseError(resp any) (isError bool, errMsg string) {
	switch v := resp.(type) {
	case map[string]any:
		if errVal, ok := v["error"]; ok && errVal != nil {
			return true, contentToString(errVal)
		}
		for _, key := range []string{"isError", "is_error"} {
			if b, ok := v[key].(bool); ok && b {
				return true, contentToString(v)
			}
		}
		return false, ""
	case string:
		if strings.Contains(strings.ToLower(v), "error") {
			return true, v
		}
		return false, ""
	default:
		return false, ""
	}
}

// geminiTimestamp tries each candidate string as an RFC3339 timestamp in
// order, returning the first one that parses successfully. Falls back to
// the current time when no candidate is present or parseable, since some
// Gemini CLI log line shapes do not carry an explicit per-line timestamp.
func geminiTimestamp(candidates ...string) time.Time {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, c); err == nil {
			return t
		}
	}
	return time.Now()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
