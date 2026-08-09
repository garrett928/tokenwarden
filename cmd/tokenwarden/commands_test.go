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
	// After the change, should show actual block contents, not just count
	if !strings.Contains(out, "09:00-17:00") {
		t.Errorf("output = %q, want the actual time block (09:00-17:00)", out)
	}
	// Should show "(every day)" for blocks with no specific days
	if !strings.Contains(out, "(every day)") {
		t.Errorf("output = %q, want '(every day)' for a block with no Days", out)
	}
}

func TestParseTimeBlock_PlainTimeRange(t *testing.T) {
	b, err := parseTimeBlock("09:00-17:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.StartMin != 540 || b.EndMin != 1020 {
		t.Errorf("got %d-%d, want 540-1020", b.StartMin, b.EndMin)
	}
	if len(b.Days) != 0 {
		t.Errorf("got %d days, want 0 (every day)", len(b.Days))
	}
}

func TestParseTimeBlock_WithDayList(t *testing.T) {
	b, err := parseTimeBlock("Mon,Tue,Wed,Thu,Fri:09:00-17:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.StartMin != 540 || b.EndMin != 1020 {
		t.Errorf("got %d-%d, want 540-1020", b.StartMin, b.EndMin)
	}
	if len(b.Days) != 5 {
		t.Errorf("got %d days, want 5", len(b.Days))
	}
	// Check that we have Monday through Friday
	expected := map[time.Weekday]bool{
		time.Monday:    true,
		time.Tuesday:   true,
		time.Wednesday: true,
		time.Thursday:  true,
		time.Friday:    true,
	}
	for _, d := range b.Days {
		if !expected[d] {
			t.Errorf("unexpected day: %v", d)
		}
	}
}

func TestParseTimeBlock_Wraparound(t *testing.T) {
	b, err := parseTimeBlock("22:00-06:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.StartMin != 1320 || b.EndMin != 360 {
		t.Errorf("got %d-%d, want 1320-360 (22:00=1320, 06:00=360)", b.StartMin, b.EndMin)
	}
	// EndMin <= StartMin is valid for wraparound
	if b.EndMin > b.StartMin {
		t.Errorf("wraparound block should have EndMin <= StartMin")
	}
}

func TestParseTimeBlock_InvalidTime(t *testing.T) {
	_, err := parseTimeBlock("25:00-26:00")
	if err == nil {
		t.Fatal("expected error for invalid hour, got none")
	}
}

func TestParseTimeBlock_InvalidWeekday(t *testing.T) {
	_, err := parseTimeBlock("Xyz:09:00-17:00")
	if err == nil {
		t.Fatal("expected error for unknown weekday, got none")
	}
	if !strings.Contains(err.Error(), "unknown weekday") {
		t.Errorf("expected 'unknown weekday' error, got: %v", err)
	}
}

func TestParseTimeBlock_OutOfRange(t *testing.T) {
	_, err := parseTimeBlock("25:00-30:00")
	if err == nil {
		t.Fatal("expected error for out-of-range minutes, got none")
	}
}

func TestFormatTimeBlock_NoDay(t *testing.T) {
	b := api.TimeBlockDTO{StartMin: 540, EndMin: 1020}
	s := formatTimeBlock(b)
	if !strings.Contains(s, "09:00") || !strings.Contains(s, "17:00") {
		t.Errorf("got %q, want times 09:00 and 17:00", s)
	}
	// formatTimeBlock outputs parseable format, not display format
	if strings.Contains(s, "every day") {
		t.Errorf("got %q, formatTimeBlock should not include '(every day)' for display", s)
	}
}

func TestFormatTimeBlock_WithDays(t *testing.T) {
	b := api.TimeBlockDTO{
		Days:     []int{int(time.Monday), int(time.Tuesday), int(time.Friday)},
		StartMin: 540,
		EndMin:   1020,
	}
	s := formatTimeBlock(b)
	if !strings.Contains(s, "Mon") || !strings.Contains(s, "Tue") || !strings.Contains(s, "Fri") {
		t.Errorf("got %q, want Mon,Tue,Fri", s)
	}
	if !strings.Contains(s, "09:00") || !strings.Contains(s, "17:00") {
		t.Errorf("got %q, want times 09:00 and 17:00", s)
	}
	if strings.Contains(s, "every day") {
		t.Errorf("got %q, should not have '(every day)' when Days is set", s)
	}
}

func TestRoundTripTimeBlock(t *testing.T) {
	tests := []string{
		"09:00-17:00",
		"Mon,Tue,Wed,Thu,Fri:09:00-17:00",
		"22:00-06:00",
		"Sat,Sun:10:00-14:00",
	}

	for _, tt := range tests {
		parsed, err := parseTimeBlock(tt)
		if err != nil {
			t.Errorf("parseTimeBlock(%q) failed: %v", tt, err)
			continue
		}

		// Convert to DTO and format back
		dto := api.TimeBlockDTO{StartMin: parsed.StartMin, EndMin: parsed.EndMin}
		for _, d := range parsed.Days {
			dto.Days = append(dto.Days, int(d))
		}
		formatted := formatTimeBlock(dto)

		// Parse the formatted version to ensure it's valid
		reparsed, err := parseTimeBlock(formatted)
		if err != nil {
			t.Errorf("re-parse of formatted %q (from %q) failed: %v", formatted, tt, err)
			continue
		}

		// Check that round-trip is equivalent (may differ in day order or format)
		if reparsed.StartMin != parsed.StartMin || reparsed.EndMin != parsed.EndMin {
			t.Errorf("round-trip of %q: time mismatch after format/parse", tt)
		}
		if len(reparsed.Days) != len(parsed.Days) {
			t.Errorf("round-trip of %q: day count mismatch", tt)
		}
	}
}
