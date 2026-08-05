package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"tokenwarden/internal/store"
)

func newTestQueue(t *testing.T) (*Queue, *store.Store) {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s), s
}

func minimalJob() store.Job {
	return store.Job{Kind: store.JobKindResearch, Prompt: "summarize the changelog"}
}

func TestEnqueue_NoDependencies_Queued(t *testing.T) {
	q, _ := newTestQueue(t)
	got, err := q.Enqueue(context.Background(), minimalJob())
	if err != nil {
		t.Fatalf("Enqueue() error: %v", err)
	}
	if got.Status != store.StatusQueued {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusQueued)
	}
}

func TestEnqueue_UnsatisfiedDependency_Blocked(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	dep, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}

	j := minimalJob()
	j.DependsOn = []string{dep.ID}
	got, err := q.Enqueue(ctx, j)
	if err != nil {
		t.Fatalf("Enqueue() error: %v", err)
	}
	if got.Status != store.StatusBlocked {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusBlocked)
	}
}

func TestEnqueue_SatisfiedDependency_Queued(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	dep, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, dep.ID, store.StatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}

	j := minimalJob()
	j.DependsOn = []string{dep.ID}
	got, err := q.Enqueue(ctx, j)
	if err != nil {
		t.Fatalf("Enqueue() error: %v", err)
	}
	if got.Status != store.StatusQueued {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusQueued)
	}
}

func TestEnqueue_FailedDependency_CancelledWithReason(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	dep, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, dep.ID, store.StatusFailed, "boom"); err != nil {
		t.Fatal(err)
	}

	j := minimalJob()
	j.DependsOn = []string{dep.ID}
	got, err := q.Enqueue(ctx, j)
	if err != nil {
		t.Fatalf("Enqueue() error: %v", err)
	}
	if got.Status != store.StatusCancelled {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusCancelled)
	}
	if got.FailureReason == "" {
		t.Error("FailureReason is empty, want an explanation naming the failed dependency")
	}
}

func TestEnqueue_MissingDependency_Error(t *testing.T) {
	q, _ := newTestQueue(t)
	j := minimalJob()
	j.DependsOn = []string{"job_does_not_exist"}
	if _, err := q.Enqueue(context.Background(), j); !errors.Is(err, ErrDependencyNotFound) {
		t.Errorf("Enqueue() error = %v, want ErrDependencyNotFound", err)
	}
}

func TestPromoteReady_PromotesWhenDependencySucceeds(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	dep, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	j := minimalJob()
	j.DependsOn = []string{dep.ID}
	blocked, err := q.Enqueue(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Status != store.StatusBlocked {
		t.Fatalf("precondition: Status = %q, want Blocked", blocked.Status)
	}

	// Nothing changed yet — sweep should be a no-op.
	n, err := q.PromoteReady(ctx)
	if err != nil {
		t.Fatalf("PromoteReady() error: %v", err)
	}
	if n != 0 {
		t.Errorf("PromoteReady() before dep succeeds = %d, want 0", n)
	}

	if err := s.UpdateStatus(ctx, dep.ID, store.StatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}

	n, err = q.PromoteReady(ctx)
	if err != nil {
		t.Fatalf("PromoteReady() error: %v", err)
	}
	if n != 1 {
		t.Errorf("PromoteReady() after dep succeeds = %d, want 1", n)
	}

	got, err := q.Get(ctx, blocked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusQueued {
		t.Errorf("Status after promotion = %q, want %q", got.Status, store.StatusQueued)
	}
}

func TestPromoteReady_CascadesCancellationWhenDependencyFails(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	dep, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	j := minimalJob()
	j.DependsOn = []string{dep.ID}
	blocked, err := q.Enqueue(ctx, j)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateStatus(ctx, dep.ID, store.StatusFailed, "boom"); err != nil {
		t.Fatal(err)
	}

	n, err := q.PromoteReady(ctx)
	if err != nil {
		t.Fatalf("PromoteReady() error: %v", err)
	}
	if n != 1 {
		t.Errorf("PromoteReady() = %d, want 1", n)
	}

	got, err := q.Get(ctx, blocked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusCancelled {
		t.Errorf("Status = %q, want %q (dependency failed permanently)", got.Status, store.StatusCancelled)
	}
}

func TestNextRunnable_OrdersByPriority(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	low := minimalJob()
	low.Priority = 1
	if _, err := q.Enqueue(ctx, low); err != nil {
		t.Fatal(err)
	}

	high := minimalJob()
	high.Priority = 10
	highJob, err := q.Enqueue(ctx, high)
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := q.NextRunnable(ctx, time.Now())
	if err != nil {
		t.Fatalf("NextRunnable() error: %v", err)
	}
	if !ok {
		t.Fatal("NextRunnable() ok = false, want true")
	}
	if got.ID != highJob.ID {
		t.Errorf("NextRunnable() = %s, want highest-priority job %s", got.ID, highJob.ID)
	}
}

func TestNextRunnable_RespectsEarliestAt(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()
	now := time.Now()

	future := now.Add(time.Hour)
	notYet := minimalJob()
	notYet.Priority = 100 // highest priority, but not eligible yet
	notYet.EarliestAt = &future
	if _, err := q.Enqueue(ctx, notYet); err != nil {
		t.Fatal(err)
	}

	ready := minimalJob()
	ready.Priority = 1
	readyJob, err := q.Enqueue(ctx, ready)
	if err != nil {
		t.Fatal(err)
	}

	got, ok, err := q.NextRunnable(ctx, now)
	if err != nil {
		t.Fatalf("NextRunnable() error: %v", err)
	}
	if !ok {
		t.Fatal("NextRunnable() ok = false, want true (the eligible lower-priority job)")
	}
	if got.ID != readyJob.ID {
		t.Errorf("NextRunnable() = %s, want %s (EarliestAt job should be skipped)", got.ID, readyJob.ID)
	}
}

func TestNextRunnable_NoneEligible(t *testing.T) {
	q, _ := newTestQueue(t)
	got, ok, err := q.NextRunnable(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("NextRunnable() error: %v", err)
	}
	if ok {
		t.Errorf("NextRunnable() on empty queue: ok = true, got %+v", got)
	}
}

func TestCancel_FromQueued(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Cancel(ctx, j.ID); err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}
	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusCancelled {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusCancelled)
	}
}

func TestCancel_AlreadyTerminal_Errors(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, j.ID, store.StatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}

	if err := q.Cancel(ctx, j.ID); !errors.Is(err, ErrAlreadyTerminal) {
		t.Errorf("Cancel() error = %v, want ErrAlreadyTerminal", err)
	}
}

func TestMarkRunning_FromQueued(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	before, err := q.MarkRunning(ctx, j.ID)
	if err != nil {
		t.Fatalf("MarkRunning() error: %v", err)
	}
	if before.Status != store.StatusQueued {
		t.Errorf("MarkRunning() returned job with Status = %q, want the pre-transition %q", before.Status, store.StatusQueued)
	}

	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusRunning {
		t.Errorf("Status after MarkRunning = %q, want %q", got.Status, store.StatusRunning)
	}
}

func TestMarkRunning_FromPausedBudget(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, j.ID, store.StatusPausedBudget, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := q.MarkRunning(ctx, j.ID); err != nil {
		t.Fatalf("MarkRunning() error: %v", err)
	}
	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusRunning {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusRunning)
	}
}

func TestMarkRunning_NotRunnable(t *testing.T) {
	q, s := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStatus(ctx, j.ID, store.StatusSucceeded, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := q.MarkRunning(ctx, j.ID); !errors.Is(err, ErrNotRunnable) {
		t.Errorf("MarkRunning() error = %v, want ErrNotRunnable", err)
	}
	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusSucceeded {
		t.Errorf("Status changed to %q after a rejected MarkRunning, want unchanged %q", got.Status, store.StatusSucceeded)
	}
}

func TestFinish_RecordsStatusAndSessionID(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.MarkRunning(ctx, j.ID); err != nil {
		t.Fatal(err)
	}

	if err := q.Finish(ctx, j.ID, store.StatusSucceeded, "", "sess-123"); err != nil {
		t.Fatalf("Finish() error: %v", err)
	}
	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusSucceeded {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusSucceeded)
	}
	if got.SessionID != "sess-123" {
		t.Errorf("SessionID = %q, want %q", got.SessionID, "sess-123")
	}
}

func TestFinish_FailureReasonWithoutSessionID(t *testing.T) {
	q, _ := newTestQueue(t)
	ctx := context.Background()

	j, err := q.Enqueue(ctx, minimalJob())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.MarkRunning(ctx, j.ID); err != nil {
		t.Fatal(err)
	}

	if err := q.Finish(ctx, j.ID, store.StatusFailed, "claude exited without a result", ""); err != nil {
		t.Fatalf("Finish() error: %v", err)
	}
	got, err := q.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusFailed {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusFailed)
	}
	if got.FailureReason != "claude exited without a result" {
		t.Errorf("FailureReason = %q, want the recorded reason", got.FailureReason)
	}
	if got.SessionID != "" {
		t.Errorf("SessionID = %q, want empty when Finish was called with no sessionID", got.SessionID)
	}
}
