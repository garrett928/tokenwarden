package main

import (
	"encoding/json"
	"strings"

	"tokenwarden/internal/api"
)

// statusLinePayload is the JSON twprobe reads on stdin when installed as
// a `statusLine` command (SPIKE-001 experiment 3). Fields beyond
// RateLimits are read only well enough to produce a minimal passthrough
// status line — twprobe doesn't attempt to replicate Claude Code's own
// status line rendering, which is far more elaborate than what SPIKE-001
// documents. RateLimits is absent on the render before the first API
// response in a session; it's present (non-nil) starting the render after.
type statusLinePayload struct {
	Model      json.RawMessage    `json:"model,omitempty"`
	Cwd        string             `json:"cwd,omitempty"`
	RateLimits *rateLimitsPayload `json:"rate_limits,omitempty"`
}

type rateLimitWindowPayload struct {
	UsedPercentage int   `json:"used_percentage"`
	ResetsAt       int64 `json:"resets_at"`
}

type rateLimitsPayload struct {
	FiveHour rateLimitWindowPayload `json:"five_hour"`
	SevenDay rateLimitWindowPayload `json:"seven_day"`
}

func parseStatusLine(data []byte) (statusLinePayload, error) {
	var p statusLinePayload
	if err := json.Unmarshal(data, &p); err != nil {
		return statusLinePayload{}, err
	}
	return p, nil
}

// extractRateLimits reports whether p carries a populated rate_limits
// object and, if so, the request body to POST to the daemon's
// /api/ground-truth endpoint.
func extractRateLimits(p statusLinePayload) (api.RecordGroundTruthRequest, bool) {
	if p.RateLimits == nil {
		return api.RecordGroundTruthRequest{}, false
	}
	return api.RecordGroundTruthRequest{
		FiveHour: api.RateLimitWindow{
			UsedPercentage: p.RateLimits.FiveHour.UsedPercentage,
			ResetsAt:       p.RateLimits.FiveHour.ResetsAt,
		},
		SevenDay: api.RateLimitWindow{
			UsedPercentage: p.RateLimits.SevenDay.UsedPercentage,
			ResetsAt:       p.RateLimits.SevenDay.ResetsAt,
		},
	}, true
}

// renderStatusLine produces a minimal passthrough line so a user who
// installs twprobe as their statusLine command still sees something
// useful, rather than a blank bar.
func renderStatusLine(p statusLinePayload) string {
	var parts []string
	if model := modelDisplayName(p.Model); model != "" {
		parts = append(parts, model)
	}
	if p.Cwd != "" {
		parts = append(parts, p.Cwd)
	}
	if len(parts) == 0 {
		return "tokenwarden probe"
	}
	return strings.Join(parts, " · ")
}

// modelDisplayName handles "model" being either a bare string or an
// object with a display_name/id field — the exact schema isn't pinned
// down by SPIKE-001, so this degrades gracefully either way rather than
// assuming one shape.
func modelDisplayName(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var asObject struct {
		DisplayName string `json:"display_name"`
		ID          string `json:"id"`
	}
	if err := json.Unmarshal(raw, &asObject); err == nil {
		if asObject.DisplayName != "" {
			return asObject.DisplayName
		}
		return asObject.ID
	}
	return ""
}
