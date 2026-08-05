package runner

import "testing"

// spikeResultJSON is copied verbatim from docs/SPIKE-001-usage-telemetry.md
// experiment 2's captured payload, so this test proves ParseEvent matches
// confirmed real `claude -p --output-format json` output exactly.
const spikeResultJSON = `{
  "type": "result", "subtype": "success",
  "duration_ms": 2728, "duration_api_ms": 3529, "ttft_ms": 1601,
  "num_turns": 1, "result": "pong", "stop_reason": "end_turn",
  "session_id": "538a1cff-example", "total_cost_usd": 0.0230845,
  "usage": {
    "input_tokens": 10,
    "cache_creation_input_tokens": 17878,
    "cache_read_input_tokens": 0,
    "output_tokens": 43,
    "cache_creation": {
      "ephemeral_1h_input_tokens": 17878,
      "ephemeral_5m_input_tokens": 0
    },
    "service_tier": "standard", "speed": "standard"
  },
  "modelUsage": {
    "claude-haiku-4-5-20251001": {
      "inputTokens": 452, "outputTokens": 57,
      "cacheReadInputTokens": 0, "cacheCreationInputTokens": 17878,
      "costUSD": 0.0230845, "contextWindow": 200000
    }
  },
  "permission_denials": [], "terminal_reason": "completed"
}`

func TestParseEvent_SpikeResultPayloadExactMatch(t *testing.T) {
	event, err := ParseEvent([]byte(spikeResultJSON))
	if err != nil {
		t.Fatalf("ParseEvent() error: %v", err)
	}
	result, ok := event.(ResultEvent)
	if !ok {
		t.Fatalf("ParseEvent() returned %T, want ResultEvent", event)
	}

	if result.IsError {
		t.Errorf("IsError = true, want false")
	}
	if result.Result != "pong" {
		t.Errorf("Result = %q, want %q", result.Result, "pong")
	}
	if result.SessionID != "538a1cff-example" {
		t.Errorf("SessionID = %q, want %q", result.SessionID, "538a1cff-example")
	}
	if result.TotalCostUSD != 0.0230845 {
		t.Errorf("TotalCostUSD = %v, want 0.0230845", result.TotalCostUSD)
	}
	if result.Usage.InputTokens != 10 || result.Usage.CacheCreationInputTokens != 17878 ||
		result.Usage.CacheReadInputTokens != 0 || result.Usage.OutputTokens != 43 {
		t.Errorf("Usage = %+v, unexpected", result.Usage)
	}
	if result.Usage.CacheCreation.Ephemeral1hInputTokens != 17878 || result.Usage.CacheCreation.Ephemeral5mInputTokens != 0 {
		t.Errorf("Usage.CacheCreation = %+v, unexpected", result.Usage.CacheCreation)
	}
	mu, ok := result.ModelUsage["claude-haiku-4-5-20251001"]
	if !ok {
		t.Fatalf("ModelUsage missing expected model key: %+v", result.ModelUsage)
	}
	if mu.CostUSD != 0.0230845 || mu.ContextWindow != 200000 || mu.CacheCreationInputTokens != 17878 {
		t.Errorf("ModelUsage entry = %+v, unexpected", mu)
	}
}

func TestParseEvent_RateLimitDetection(t *testing.T) {
	line := []byte(`{"type":"system","subtype":"api_retry","error":"rate_limit","session_id":"abc"}`)
	event, err := ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent() error: %v", err)
	}
	sysEvent, ok := event.(SystemEvent)
	if !ok {
		t.Fatalf("ParseEvent() returned %T, want SystemEvent", event)
	}
	if !sysEvent.IsRateLimit() {
		t.Errorf("IsRateLimit() = false, want true for %+v", sysEvent)
	}

	other := []byte(`{"type":"system","subtype":"init","session_id":"abc"}`)
	event2, err := ParseEvent(other)
	if err != nil {
		t.Fatalf("ParseEvent() error: %v", err)
	}
	if event2.(SystemEvent).IsRateLimit() {
		t.Errorf("IsRateLimit() = true for a non-retry system event, want false")
	}
}

func TestParseEvent_UnknownTypeIsNotFatal(t *testing.T) {
	line := []byte(`{"type":"telemetry_ping","foo":"bar"}`)
	event, err := ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent() error: %v, want nil (unknown type must not be fatal)", err)
	}
	unknown, ok := event.(UnknownEvent)
	if !ok {
		t.Fatalf("ParseEvent() returned %T, want UnknownEvent", event)
	}
	if unknown.Type != "telemetry_ping" {
		t.Errorf("Type = %q, want %q", unknown.Type, "telemetry_ping")
	}
}

func TestParseEvent_NonJSONLineErrors(t *testing.T) {
	_, err := ParseEvent([]byte("not json at all"))
	if err == nil {
		t.Error("ParseEvent() error = nil, want error for a non-JSON line")
	}
}

func TestParseEvent_MissingOptionalFieldsZeroValue(t *testing.T) {
	line := []byte(`{"type":"result","subtype":"success","session_id":"abc","result":"ok"}`)
	event, err := ParseEvent(line)
	if err != nil {
		t.Fatalf("ParseEvent() error: %v", err)
	}
	result := event.(ResultEvent)
	if result.ModelUsage != nil {
		t.Errorf("ModelUsage = %+v, want nil/zero when absent from JSON", result.ModelUsage)
	}
	if result.Usage.CacheCreation.Ephemeral1hInputTokens != 0 {
		t.Errorf("Usage.CacheCreation should zero-value when usage is entirely absent")
	}
	if result.TotalCostUSD != 0 {
		t.Errorf("TotalCostUSD = %v, want 0 when absent", result.TotalCostUSD)
	}
}
