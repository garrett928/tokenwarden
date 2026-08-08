package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"tokenwarden/internal/api"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything it printed. printGroundTruth (like the rest of this file's
// CLI commands) writes directly to os.Stdout rather than an injected
// writer, so this is the simplest way to assert on its output without a
// wider refactor.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestPrintGroundTruth_NilPointsToInstall(t *testing.T) {
	out := captureStdout(t, func() { printGroundTruth(nil) })
	if !strings.Contains(out, "tokenwarden probe install") {
		t.Errorf("output = %q, want a pointer to 'tokenwarden probe install' when no reading exists", out)
	}
}

func TestPrintGroundTruth_PresentShowsBothWindows(t *testing.T) {
	gt := &api.GroundTruthResponse{
		FiveHour:   api.RateLimitWindow{UsedPercentage: 89, ResetsAt: time.Now().Add(time.Hour).Unix()},
		SevenDay:   api.RateLimitWindow{UsedPercentage: 22, ResetsAt: time.Now().Add(48 * time.Hour).Unix()},
		ObservedAt: time.Now(),
		AgeSeconds: 5,
	}
	out := captureStdout(t, func() { printGroundTruth(gt) })
	if !strings.Contains(out, "89%") {
		t.Errorf("output = %q, want the five-hour used percentage (89%%)", out)
	}
	if !strings.Contains(out, "22%") {
		t.Errorf("output = %q, want the seven-day used percentage (22%%)", out)
	}
}

func TestPrintSchedulerConfig(t *testing.T) {
	budget := 15.5
	cfg := api.SchedulerConfigResponse{
		Enabled:        true,
		Aggressiveness: 65,
		MaxBudgetUSD:   &budget,
		ReservedBlocks: []api.TimeBlockDTO{{StartMin: 540, EndMin: 1020}},
	}
	out := captureStdout(t, func() { printSchedulerConfig(cfg) })
	if !strings.Contains(out, "true") {
		t.Errorf("output = %q, want Enabled=true reflected", out)
	}
	if !strings.Contains(out, "65%") {
		t.Errorf("output = %q, want aggressiveness (65%%)", out)
	}
	if !strings.Contains(out, "$15.50") {
		t.Errorf("output = %q, want the max budget ($15.50)", out)
	}
	if !strings.Contains(out, "1 configured") {
		t.Errorf("output = %q, want the reserved block count", out)
	}
}
