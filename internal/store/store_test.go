package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	// A unique file-backed in-memory-mode-ish DB per test avoids cross-test
	// interference; SQLite's true ":memory:" is fine too since MaxOpenConns
	// is pinned to 1 (see store.go), which keeps the single in-memory
	// connection stable for the test's lifetime.
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrate_CreatesJobsTable(t *testing.T) {
	s := openTestStore(t)
	jobs, err := s.ListJobs(context.Background(), ListFilter{})
	if err != nil {
		t.Fatalf("ListJobs() on fresh db error: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("fresh db has %d jobs, want 0", len(jobs))
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	// Opening twice against the same file must not fail on re-applying
	// migrations. Use a temp file since ":memory:" doesn't persist across
	// separate Open calls.
	path := t.TempDir() + "/tokenwarden.db"

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open() error: %v", err)
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open() (re-applying migrations) error: %v", err)
	}
	defer s2.Close()
}

func minimalJob() Job {
	return Job{Kind: JobKindResearch, Prompt: "summarize the changelog"}
}

func TestCreateJob_AssignsIDAndTimestamps(t *testing.T) {
	s := openTestStore(t)
	before := time.Now().Add(-time.Second)

	got, err := s.CreateJob(context.Background(), minimalJob())
	if err != nil {
		t.Fatalf("CreateJob() error: %v", err)
	}
	if got.ID == "" {
		t.Error("CreateJob() did not assign an ID")
	}
	if got.Status != StatusQueued {
		t.Errorf("Status = %q, want default %q", got.Status, StatusQueued)
	}
	if got.CreatedAt.Before(before) {
		t.Errorf("CreatedAt = %v, want after %v", got.CreatedAt, before)
	}
	if !got.UpdatedAt.Equal(got.CreatedAt) {
		t.Errorf("UpdatedAt = %v, want equal to CreatedAt %v", got.UpdatedAt, got.CreatedAt)
	}
}

func TestCreateJob_RejectsEmptyPrompt(t *testing.T) {
	s := openTestStore(t)
	j := minimalJob()
	j.Prompt = "   "
	if _, err := s.CreateJob(context.Background(), j); err == nil {
		t.Error("CreateJob() with blank prompt: want error, got nil")
	}
}

func TestCreateJob_RejectsUnknownKind(t *testing.T) {
	s := openTestStore(t)
	j := minimalJob()
	j.Kind = "not-a-real-kind"
	if _, err := s.CreateJob(context.Background(), j); err == nil {
		t.Error("CreateJob() with unknown kind: want error, got nil")
	}
}

func TestCreateJob_RejectsDuplicateID(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	j := minimalJob()
	j.ID = "job_fixed"

	if _, err := s.CreateJob(ctx, j); err != nil {
		t.Fatalf("first CreateJob() error: %v", err)
	}
	if _, err := s.CreateJob(ctx, j); err == nil {
		t.Error("CreateJob() with duplicate ID: want error, got nil")
	}
}

func TestGetJob_RoundTripsAllFields(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	earliest := time.Now().Add(time.Hour).Truncate(time.Second).UTC()
	deadline := time.Now().Add(24 * time.Hour).Truncate(time.Second).UTC()
	budget := 5.50

	in := Job{
		Kind:         JobKindCode,
		Prompt:       "add input validation to the login handler",
		Workspace:    "/repos/app",
		Model:        "sonnet",
		Effort:       "high",
		Attachments:  []Attachment{{Path: "/tmp/screenshot.png", Name: "screenshot.png"}},
		Steps:        []string{"add validation", "write tests"},
		Resumable:    true,
		Priority:     10,
		EarliestAt:   &earliest,
		DeadlineAt:   &deadline,
		MaxBudgetUSD: &budget,
		DependsOn:    []string{"job_other"},
	}

	created, err := s.CreateJob(ctx, in)
	if err != nil {
		t.Fatalf("CreateJob() error: %v", err)
	}

	got, err := s.GetJob(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}

	if got.Kind != in.Kind || got.Prompt != in.Prompt || got.Workspace != in.Workspace ||
		got.Model != in.Model || got.Effort != in.Effort || got.Resumable != in.Resumable ||
		got.Priority != in.Priority {
		t.Errorf("scalar fields did not round-trip: got %+v", got)
	}
	if len(got.Attachments) != 1 || got.Attachments[0] != in.Attachments[0] {
		t.Errorf("Attachments = %+v, want %+v", got.Attachments, in.Attachments)
	}
	if len(got.Steps) != 2 || got.Steps[0] != "add validation" {
		t.Errorf("Steps = %+v, want %+v", got.Steps, in.Steps)
	}
	if len(got.DependsOn) != 1 || got.DependsOn[0] != "job_other" {
		t.Errorf("DependsOn = %+v, want %+v", got.DependsOn, in.DependsOn)
	}
	if got.EarliestAt == nil || !got.EarliestAt.Equal(earliest) {
		t.Errorf("EarliestAt = %v, want %v", got.EarliestAt, earliest)
	}
	if got.DeadlineAt == nil || !got.DeadlineAt.Equal(deadline) {
		t.Errorf("DeadlineAt = %v, want %v", got.DeadlineAt, deadline)
	}
	if got.MaxBudgetUSD == nil || *got.MaxBudgetUSD != budget {
		t.Errorf("MaxBudgetUSD = %v, want %v", got.MaxBudgetUSD, budget)
	}
}

func TestGetJob_NotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.GetJob(context.Background(), "job_nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetJob() error = %v, want ErrNotFound", err)
	}
}

func TestGetJob_NilSlicesRoundTripAsEmpty(t *testing.T) {
	// A job created with nil Attachments/Steps/DependsOn should read back
	// as empty slices, not nil — callers (the API JSON layer especially)
	// shouldn't have to special-case nil vs empty.
	s := openTestStore(t)
	ctx := context.Background()

	created, err := s.CreateJob(ctx, minimalJob())
	if err != nil {
		t.Fatalf("CreateJob() error: %v", err)
	}
	got, err := s.GetJob(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetJob() error: %v", err)
	}
	if got.Attachments == nil {
		t.Error("Attachments is nil, want empty slice")
	}
	if got.Steps == nil {
		t.Error("Steps is nil, want empty slice")
	}
	if got.DependsOn == nil {
		t.Error("DependsOn is nil, want empty slice")
	}
}

func TestListJobs_OrdersByPriorityThenCreatedAt(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	low := minimalJob()
	low.Priority = 1
	lowJob, err := s.CreateJob(ctx, low)
	if err != nil {
		t.Fatal(err)
	}

	high := minimalJob()
	high.Priority = 10
	highJob, err := s.CreateJob(ctx, high)
	if err != nil {
		t.Fatal(err)
	}

	mid := minimalJob()
	mid.Priority = 5
	midJob, err := s.CreateJob(ctx, mid)
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.ListJobs(ctx, ListFilter{})
	if err != nil {
		t.Fatalf("ListJobs() error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListJobs() returned %d jobs, want 3", len(got))
	}
	wantOrder := []string{highJob.ID, midJob.ID, lowJob.ID}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("position %d: got job %s, want %s", i, got[i].ID, id)
		}
	}
}

func TestListJobs_FiltersByStatus(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	queued, err := s.CreateJob(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	running, err := s.CreateJob(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, running.ID, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListJobs(ctx, ListFilter{Statuses: []Status{StatusRunning}})
	if err != nil {
		t.Fatalf("ListJobs() error: %v", err)
	}
	if len(got) != 1 || got[0].ID != running.ID {
		t.Errorf("ListJobs(running) = %+v, want just %s", got, running.ID)
	}

	got, err = s.ListJobs(ctx, ListFilter{Statuses: []Status{StatusQueued, StatusRunning}})
	if err != nil {
		t.Fatalf("ListJobs() error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ListJobs(queued, running) returned %d jobs, want 2", len(got))
	}
	_ = queued
}

func TestGetJobs_BatchFetch(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	a, err := s.CreateJob(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateJob(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.GetJobs(ctx, []string{a.ID, b.ID, "job_does_not_exist"})
	if err != nil {
		t.Fatalf("GetJobs() error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("GetJobs() returned %d jobs, want 2 (missing ID silently skipped)", len(got))
	}
}

func TestGetJobs_EmptyInput(t *testing.T) {
	s := openTestStore(t)
	got, err := s.GetJobs(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetJobs(nil) error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("GetJobs(nil) = %+v, want empty", got)
	}
}

func TestUpdateStatus_RecordsFailureReason(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	created, err := s.CreateJob(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateStatus(ctx, created.ID, StatusFailed, "claude exited 1: rate_limit"); err != nil {
		t.Fatalf("UpdateStatus() error: %v", err)
	}

	got, err := s.GetJob(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", got.Status, StatusFailed)
	}
	if got.FailureReason != "claude exited 1: rate_limit" {
		t.Errorf("FailureReason = %q, want the recorded reason", got.FailureReason)
	}
	if !got.UpdatedAt.After(got.CreatedAt) && !got.UpdatedAt.Equal(got.CreatedAt) {
		t.Errorf("UpdatedAt = %v, want >= CreatedAt %v", got.UpdatedAt, got.CreatedAt)
	}
}

func TestUpdateStatus_NotFound(t *testing.T) {
	s := openTestStore(t)
	err := s.UpdateStatus(context.Background(), "job_nonexistent", StatusRunning, "")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateStatus() error = %v, want ErrNotFound", err)
	}
}

func TestUpdateSessionID(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	created, err := s.CreateJob(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSessionID(ctx, created.ID, "sess-abc-123"); err != nil {
		t.Fatalf("UpdateSessionID() error: %v", err)
	}
	got, err := s.GetJob(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "sess-abc-123" {
		t.Errorf("SessionID = %q, want %q", got.SessionID, "sess-abc-123")
	}
}

func TestDeleteJob(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	created, err := s.CreateJob(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteJob(ctx, created.ID); err != nil {
		t.Fatalf("DeleteJob() error: %v", err)
	}
	if _, err := s.GetJob(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetJob() after delete: error = %v, want ErrNotFound", err)
	}
}

func TestDeleteJob_NotFound(t *testing.T) {
	s := openTestStore(t)
	err := s.DeleteJob(context.Background(), "job_nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteJob() error = %v, want ErrNotFound", err)
	}
}

func TestStatus_Terminal(t *testing.T) {
	terminal := []Status{StatusSucceeded, StatusFailed, StatusCancelled}
	for _, s := range terminal {
		if !s.Terminal() {
			t.Errorf("Status(%q).Terminal() = false, want true", s)
		}
	}
	nonTerminal := []Status{StatusQueued, StatusBlocked, StatusRunning, StatusPausedBudget, StatusDeferredOversized}
	for _, s := range nonTerminal {
		if s.Terminal() {
			t.Errorf("Status(%q).Terminal() = true, want false", s)
		}
	}
}
