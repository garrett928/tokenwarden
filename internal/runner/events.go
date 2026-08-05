package runner

import (
	"encoding/json"
	"fmt"
)

// Event is one line of `claude -p --output-format stream-json` output.
// Concrete types below cover the event shapes tokenwarden cares about;
// anything else decodes as UnknownEvent rather than failing, which is the
// forward-compatibility behavior FR-EXEC-3 asks for — a new or renamed
// event type from a future claude version must degrade gracefully, not
// crash the runner.
type Event interface {
	// EventType returns the event's "type" field verbatim.
	EventType() string
}

// SystemEvent covers "system" events, notably the "api_retry" subtype that
// carries the rate-limit signal FR-SCHED-5 calls the "authoritative
// backoff trigger". The runner surfaces these; it does not act on them —
// retry/backoff policy belongs to a future scheduler.
type SystemEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype,omitempty"`
	Error     string `json:"error,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

func (e SystemEvent) EventType() string { return e.Type }

// IsRateLimit reports whether this event is the authoritative rate-limit
// signal (system/api_retry with error "rate_limit") per REQUIREMENTS.md
// §4.2 and FR-SCHED-5.
func (e SystemEvent) IsRateLimit() bool {
	return e.Subtype == "api_retry" && e.Error == "rate_limit"
}

// AssistantEvent covers "assistant" turn events. tokenwarden doesn't parse
// the message body today — Result is built from the terminal "result"
// event — but keeping the raw message available lets a future caller (e.g.
// live-streaming to a UI, FR-ART-2) forward it without re-parsing.
type AssistantEvent struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message,omitempty"`
}

func (e AssistantEvent) EventType() string { return e.Type }

// UserEvent covers "user" events (tool results fed back to the model).
type UserEvent struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message,omitempty"`
}

func (e UserEvent) EventType() string { return e.Type }

// CacheCreationSplit breaks cache-creation tokens down by TTL, per
// SPIKE-001 experiment 2's captured payload.
type CacheCreationSplit struct {
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
}

// Usage is the exact per-run token ledger, field-for-field matching
// SPIKE-001's captured `-p --output-format json` payload.
type Usage struct {
	InputTokens              int                `json:"input_tokens"`
	CacheCreationInputTokens int                `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int                `json:"cache_read_input_tokens"`
	OutputTokens             int                `json:"output_tokens"`
	CacheCreation            CacheCreationSplit `json:"cache_creation"`
	ServiceTier              string             `json:"service_tier,omitempty"`
}

// ModelUsage is one model's contribution to a multi-model run, keyed by
// model string in ResultEvent.ModelUsage.
type ModelUsage struct {
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	CostUSD                  float64 `json:"costUSD"`
	ContextWindow            int     `json:"contextWindow,omitempty"`
}

// ResultEvent is the terminal "result" event every successful `claude -p`
// invocation emits exactly once, carrying the ledger data Run() surfaces
// as a Result.
type ResultEvent struct {
	Type          string                `json:"type"`
	Subtype       string                `json:"subtype,omitempty"`
	IsError       bool                  `json:"is_error"`
	Result        string                `json:"result,omitempty"`
	StopReason    string                `json:"stop_reason,omitempty"`
	SessionID     string                `json:"session_id"`
	NumTurns      int                   `json:"num_turns"`
	DurationMS    int64                 `json:"duration_ms"`
	DurationAPIMS int64                 `json:"duration_api_ms"`
	TotalCostUSD  float64               `json:"total_cost_usd"`
	Usage         Usage                 `json:"usage"`
	ModelUsage    map[string]ModelUsage `json:"modelUsage,omitempty"`
}

func (e ResultEvent) EventType() string { return e.Type }

// UnknownEvent is any event whose "type" this package doesn't have a
// concrete struct for — a new event type introduced by a future claude
// version, or a renamed one. Raw holds the original line so a caller can
// still forward or log it.
type UnknownEvent struct {
	Type string
	Raw  json.RawMessage
}

func (e UnknownEvent) EventType() string { return e.Type }

// ParseEvent decodes one line of stream-json output into its concrete
// Event type, falling back to UnknownEvent for any "type" this package
// doesn't recognize. It returns an error only when line isn't valid JSON
// at all — the caller (Run) treats that as a single skippable line, not a
// reason to abort the whole run, since a stray non-JSON line on stdout
// shouldn't take down parsing of everything that follows.
func ParseEvent(line []byte) (Event, error) {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return nil, fmt.Errorf("parsing stream-json line: %w", err)
	}

	switch envelope.Type {
	case "system":
		var e SystemEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("parsing system event: %w", err)
		}
		return e, nil
	case "assistant":
		var e AssistantEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("parsing assistant event: %w", err)
		}
		return e, nil
	case "user":
		var e UserEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("parsing user event: %w", err)
		}
		return e, nil
	case "result":
		var e ResultEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("parsing result event: %w", err)
		}
		return e, nil
	default:
		raw := append(json.RawMessage(nil), line...)
		return UnknownEvent{Type: envelope.Type, Raw: raw}, nil
	}
}
