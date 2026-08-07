package store

import (
	"context"
	"testing"
	"time"
)

func TestRecordUsage_AssignsIDAndTimestamp(t *testing.T) {
	s := openTestStore(t)
	before := time.Now().Add(-time.Second)

	got, err := s.RecordUsage(context.Background(), UsageEntry{JobID: "job_x", Model: "haiku", CostUSD: 0.01})
	if err != nil {
		t.Fatalf("RecordUsage() error: %v", err)
	}
	if got.ID == "" {
		t.Error("RecordUsage() did not assign an ID")
	}
	if got.RecordedAt.Before(before) {
		t.Errorf("RecordedAt = %v, want after %v", got.RecordedAt, before)
	}
}

func TestRecordUsage_RespectsExplicitRecordedAt(t *testing.T) {
	s := openTestStore(t)
	explicit := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	got, err := s.RecordUsage(context.Background(), UsageEntry{JobID: "job_x", RecordedAt: explicit})
	if err != nil {
		t.Fatalf("RecordUsage() error: %v", err)
	}
	if !got.RecordedAt.Equal(explicit) {
		t.Errorf("RecordedAt = %v, want %v", got.RecordedAt, explicit)
	}
}

func TestListUsageSince_FiltersByTimeAndOrdersOldestFirst(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	old := UsageEntry{JobID: "job_old", RecordedAt: now.Add(-10 * time.Hour), CostUSD: 1}
	mid := UsageEntry{JobID: "job_mid", RecordedAt: now.Add(-2 * time.Hour), CostUSD: 2}
	recent := UsageEntry{JobID: "job_recent", RecordedAt: now.Add(-1 * time.Hour), CostUSD: 3}

	for _, e := range []UsageEntry{old, mid, recent} {
		if _, err := s.RecordUsage(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.ListUsageSince(ctx, now.Add(-5*time.Hour))
	if err != nil {
		t.Fatalf("ListUsageSince() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListUsageSince() returned %d entries, want 2", len(got))
	}
	if got[0].JobID != "job_mid" || got[1].JobID != "job_recent" {
		t.Errorf("ListUsageSince() order = [%s, %s], want [job_mid, job_recent]", got[0].JobID, got[1].JobID)
	}
}

func TestListUsageSince_EmptyWhenNothingRecorded(t *testing.T) {
	s := openTestStore(t)
	got, err := s.ListUsageSince(context.Background(), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("ListUsageSince() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListUsageSince() = %+v, want empty", got)
	}
}

func TestRecordUsage_RoundTripsAllFields(t *testing.T) {
	s := openTestStore(t)
	in := UsageEntry{
		JobID:                    "job_abc",
		Model:                    "claude-haiku-4-5-20251001",
		InputTokens:              10,
		CacheCreationInputTokens: 17878,
		CacheReadInputTokens:     5,
		OutputTokens:             43,
		CostUSD:                  0.0230845,
	}
	created, err := s.RecordUsage(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordUsage() error: %v", err)
	}

	got, err := s.ListUsageSince(context.Background(), created.RecordedAt.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("ListUsageSince() returned %d entries, want 1", len(got))
	}
	e := got[0]
	if e.JobID != in.JobID || e.Model != in.Model || e.InputTokens != in.InputTokens ||
		e.CacheCreationInputTokens != in.CacheCreationInputTokens || e.CacheReadInputTokens != in.CacheReadInputTokens ||
		e.OutputTokens != in.OutputTokens || e.CostUSD != in.CostUSD {
		t.Errorf("round-tripped entry = %+v, want fields matching %+v", e, in)
	}
}

func TestRecordUsageIfNew_IdempotentOnSameSourceUUID(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	entry := UsageEntry{JobID: "interactive:sess-1", SourceUUID: "msg-uuid-1", InputTokens: 10, OutputTokens: 5}

	inserted1, err := s.RecordUsageIfNew(ctx, entry)
	if err != nil {
		t.Fatalf("first RecordUsageIfNew() error: %v", err)
	}
	if !inserted1 {
		t.Error("first RecordUsageIfNew() inserted = false, want true")
	}

	inserted2, err := s.RecordUsageIfNew(ctx, entry)
	if err != nil {
		t.Fatalf("second RecordUsageIfNew() error: %v", err)
	}
	if inserted2 {
		t.Error("second RecordUsageIfNew() with the same SourceUUID inserted = true, want false (idempotent)")
	}

	got, err := s.ListUsageSince(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("ListUsageSince() returned %d entries, want exactly 1 (no duplicate)", len(got))
	}
}

func TestRecordUsageIfNew_RequiresSourceUUID(t *testing.T) {
	s := openTestStore(t)
	_, err := s.RecordUsageIfNew(context.Background(), UsageEntry{JobID: "x"})
	if err == nil {
		t.Error("RecordUsageIfNew() with empty SourceUUID: want error, got nil")
	}
}
