package store

import (
	"context"
	"math"
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

func TestCompletedJobUsageTotals_SumsPerJobAndScopesByKindAndStatus(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	research, err := s.CreateJob(ctx, Job{Kind: JobKindResearch, Prompt: "research it"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, research.ID, StatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}
	// Two usage rows (e.g. two models) for the same job — must sum into one
	// JobUsageTotal, not two.
	if _, err := s.RecordUsage(ctx, UsageEntry{JobID: research.ID, Model: "haiku", InputTokens: 100, OutputTokens: 20, CostUSD: 0.1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordUsage(ctx, UsageEntry{JobID: research.ID, Model: "sonnet", InputTokens: 200, OutputTokens: 30, CostUSD: 0.2}); err != nil {
		t.Fatal(err)
	}

	code, err := s.CreateJob(ctx, Job{Kind: JobKindCode, Prompt: "code it"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, code.ID, StatusFailed, "oops"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordUsage(ctx, UsageEntry{JobID: code.ID, InputTokens: 500, CostUSD: 1.0}); err != nil {
		t.Fatal(err)
	}

	stillRunning, err := s.CreateJob(ctx, Job{Kind: JobKindResearch, Prompt: "not done yet"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, stillRunning.ID, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordUsage(ctx, UsageEntry{JobID: stillRunning.ID, InputTokens: 9999, CostUSD: 99}); err != nil {
		t.Fatal(err)
	}

	got, err := s.CompletedJobUsageTotals(ctx, JobKindResearch)
	if err != nil {
		t.Fatalf("CompletedJobUsageTotals() error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("CompletedJobUsageTotals(research) returned %d totals, want 1 (running job excluded, code job wrong kind)", len(got))
	}
	if got[0].JobID != research.ID {
		t.Errorf("JobID = %s, want %s", got[0].JobID, research.ID)
	}
	if got[0].Tokens != 350 {
		t.Errorf("Tokens = %d, want 350 (100+20+200+30, summed across both model rows)", got[0].Tokens)
	}
	if math.Abs(got[0].CostUSD-0.3) > 1e-9 {
		t.Errorf("CostUSD = %v, want ~0.3", got[0].CostUSD)
	}

	gotCode, err := s.CompletedJobUsageTotals(ctx, JobKindCode)
	if err != nil {
		t.Fatalf("CompletedJobUsageTotals() error: %v", err)
	}
	if len(gotCode) != 1 || gotCode[0].JobID != code.ID {
		t.Fatalf("CompletedJobUsageTotals(code) = %+v, want just the failed code job", gotCode)
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
