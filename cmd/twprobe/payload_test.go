package main

import "testing"

func TestParseStatusLine_RenderOne_NoRateLimits(t *testing.T) {
	// SPIKE-001 experiment 3, render 1: rate_limits absent before the
	// first API response.
	data := []byte(`{
		"context_window": 200000, "cost": {"total_cost_usd": 0},
		"cwd": "/repos/app", "model": "claude-sonnet-4-5",
		"session_id": "abc", "version": "2.1.152"
	}`)
	p, err := parseStatusLine(data)
	if err != nil {
		t.Fatalf("parseStatusLine() error: %v", err)
	}
	if p.RateLimits != nil {
		t.Errorf("RateLimits = %+v, want nil (render before first API response)", p.RateLimits)
	}
	if p.Cwd != "/repos/app" {
		t.Errorf("Cwd = %q, want /repos/app", p.Cwd)
	}
}

func TestParseStatusLine_RenderTwo_HasRateLimits(t *testing.T) {
	// SPIKE-001 experiment 3, render 2 — the literal captured payload.
	data := []byte(`{
		"rate_limits": {
			"five_hour": {"used_percentage": 89, "resets_at": 1785642600},
			"seven_day": {"used_percentage": 22, "resets_at": 1786104000}
		}
	}`)
	p, err := parseStatusLine(data)
	if err != nil {
		t.Fatalf("parseStatusLine() error: %v", err)
	}
	if p.RateLimits == nil {
		t.Fatal("RateLimits is nil, want populated")
	}
	if p.RateLimits.FiveHour.UsedPercentage != 89 || p.RateLimits.FiveHour.ResetsAt != 1785642600 {
		t.Errorf("FiveHour = %+v, want {89, 1785642600}", p.RateLimits.FiveHour)
	}
	if p.RateLimits.SevenDay.UsedPercentage != 22 || p.RateLimits.SevenDay.ResetsAt != 1786104000 {
		t.Errorf("SevenDay = %+v, want {22, 1786104000}", p.RateLimits.SevenDay)
	}
}

func TestParseStatusLine_MalformedJSON(t *testing.T) {
	_, err := parseStatusLine([]byte("not json"))
	if err == nil {
		t.Error("parseStatusLine() error = nil, want error for malformed input")
	}
}

func TestExtractRateLimits_AbsentReturnsNotOK(t *testing.T) {
	p := statusLinePayload{}
	_, ok := extractRateLimits(p)
	if ok {
		t.Error("extractRateLimits() ok = true, want false when RateLimits is nil")
	}
}

func TestExtractRateLimits_PresentReturnsRequest(t *testing.T) {
	p := statusLinePayload{
		RateLimits: &rateLimitsPayload{
			FiveHour: rateLimitWindowPayload{UsedPercentage: 89, ResetsAt: 1785642600},
			SevenDay: rateLimitWindowPayload{UsedPercentage: 22, ResetsAt: 1786104000},
		},
	}
	req, ok := extractRateLimits(p)
	if !ok {
		t.Fatal("extractRateLimits() ok = false, want true")
	}
	if req.FiveHour.UsedPercentage != 89 || req.SevenDay.UsedPercentage != 22 {
		t.Errorf("req = %+v, want 89/22", req)
	}
}

func TestRenderStatusLine_ModelAsString(t *testing.T) {
	p, err := parseStatusLine([]byte(`{"model": "claude-sonnet-4-5", "cwd": "/repos/app"}`))
	if err != nil {
		t.Fatal(err)
	}
	got := renderStatusLine(p)
	want := "claude-sonnet-4-5 · /repos/app"
	if got != want {
		t.Errorf("renderStatusLine() = %q, want %q", got, want)
	}
}

func TestRenderStatusLine_ModelAsObject(t *testing.T) {
	p, err := parseStatusLine([]byte(`{"model": {"display_name": "Sonnet 4.5", "id": "claude-sonnet-4-5"}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := renderStatusLine(p)
	if got != "Sonnet 4.5" {
		t.Errorf("renderStatusLine() = %q, want %q", got, "Sonnet 4.5")
	}
}

func TestRenderStatusLine_FallbackWhenEmpty(t *testing.T) {
	got := renderStatusLine(statusLinePayload{})
	if got != "tokenwarden probe" {
		t.Errorf("renderStatusLine() = %q, want the fallback string", got)
	}
}
