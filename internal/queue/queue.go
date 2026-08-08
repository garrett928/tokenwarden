// Package queue implements job lifecycle rules on top of internal/store:
// dependency gating and picking the next job to run. It holds no
// persistence or HTTP concerns of its own — internal/api depends on this
// package, not the other way around.
//
// Phase 2 scope: no pacing here. NextRunnable just returns the
// highest-priority eligible job; the budget-aware admission control from
// REQUIREMENTS.md §6 (aggressiveness, reserved blocks, oversized-job
// fitting) lands in internal/scheduler on top of this in a later phase.
package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tokenwarden/internal/store"
)

// ErrDependencyNotFound is returned when a job names a DependsOn ID that
// doesn't exist in the store.
var ErrDependencyNotFound = errors.New("dependency job not found")

// ErrAlreadyTerminal is returned by Cancel when the job has already reached
// a terminal status and can't be cancelled.
var ErrAlreadyTerminal = errors.New("job already in a terminal state")

// ErrNotRunnable is returned by MarkRunning when the job isn't in a status
// that can transition to Running.
var ErrNotRunnable = errors.New("job is not in a dispatchable state")

// Queue wraps a *store.Store with job lifecycle rules.
type Queue struct {
	store *store.Store
}

// New wraps the given store.
func New(s *store.Store) *Queue {
	return &Queue{store: s}
}

// Enqueue validates a job's dependencies and persists it with the correct
// initial status: Queued if it has none or they're all already succeeded,
// Blocked if some are still pending, or Cancelled if any dependency has
// already failed or been cancelled — a job whose dependency can never
// succeed can never run either, and sitting in Blocked forever would hide
// that rather than surface it.
func (q *Queue) Enqueue(ctx context.Context, job store.Job) (store.Job, error) {
	deps, err := q.resolveDeps(ctx, job.DependsOn)
	if err != nil {
		return store.Job{}, err
	}

	status, reason := computeStatus(deps)
	job.Status = status
	job.FailureReason = reason

	created, err := q.store.CreateJob(ctx, job)
	if err != nil {
		return store.Job{}, err
	}
	return created, nil
}

// Get fetches a single job.
func (q *Queue) Get(ctx context.Context, id string) (store.Job, error) {
	return q.store.GetJob(ctx, id)
}

// List returns jobs matching filter.
func (q *Queue) List(ctx context.Context, filter store.ListFilter) ([]store.Job, error) {
	return q.store.ListJobs(ctx, filter)
}

// PromoteReady sweeps every Blocked job, re-checks its dependencies, and
// transitions it to Queued (all dependencies succeeded), Cancelled (a
// dependency failed or was cancelled since this job was blocked), or leaves
// it Blocked (still waiting). It returns how many jobs changed status.
//
// This is a sweep rather than an event-driven promotion because Phase 2 has
// no scheduler loop yet to drive it; call it after any job's status changes
// to Succeeded/Failed/Cancelled, or on a timer. Errors for individual jobs
// (e.g. a dependency that was deleted) are collected and joined rather than
// aborting the whole sweep.
func (q *Queue) PromoteReady(ctx context.Context) (int, error) {
	blocked, err := q.store.ListJobs(ctx, store.ListFilter{Statuses: []store.Status{store.StatusBlocked}})
	if err != nil {
		return 0, fmt.Errorf("listing blocked jobs: %w", err)
	}

	var promoted int
	var errs []error
	for _, b := range blocked {
		deps, err := q.resolveDeps(ctx, b.DependsOn)
		if err != nil {
			errs = append(errs, fmt.Errorf("job %s: %w", b.ID, err))
			continue
		}

		status, reason := computeStatus(deps)
		if status == store.StatusBlocked {
			continue // still waiting, nothing to do
		}
		if err := q.store.UpdateStatus(ctx, b.ID, status, reason); err != nil {
			errs = append(errs, fmt.Errorf("job %s: %w", b.ID, err))
			continue
		}
		promoted++
	}

	return promoted, errors.Join(errs...)
}

// NextRunnable returns the highest-priority Queued job whose EarliestAt (if
// any) has passed as of now. Jobs are already ordered by priority (highest
// first) then creation time by the store, so this just walks that order and
// applies the EarliestAt gate. ok is false when nothing is eligible yet.
func (q *Queue) NextRunnable(ctx context.Context, now time.Time) (job store.Job, ok bool, err error) {
	candidates, err := q.store.ListJobs(ctx, store.ListFilter{Statuses: []store.Status{store.StatusQueued}})
	if err != nil {
		return store.Job{}, false, fmt.Errorf("listing queued jobs: %w", err)
	}

	for _, j := range candidates {
		if j.EarliestAt != nil && j.EarliestAt.After(now) {
			continue
		}
		return j, true, nil
	}
	return store.Job{}, false, nil
}

// Cancel transitions a non-terminal job to Cancelled. Cancelling a job
// that's already finished (succeeded, failed, or previously cancelled) is
// an error rather than a silent no-op, since it usually means the caller's
// idea of the job's state is stale.
func (q *Queue) Cancel(ctx context.Context, id string) error {
	j, err := q.store.GetJob(ctx, id)
	if err != nil {
		return err
	}
	if j.Status.Terminal() {
		return fmt.Errorf("%w: job %s is already %s", ErrAlreadyTerminal, id, j.Status)
	}
	return q.store.UpdateStatus(ctx, id, store.StatusCancelled, "cancelled by user")
}

// MarkRunning transitions a Queued or PausedBudget job to Running and
// returns the job as it stood just before the transition (so the caller —
// internal/dispatch — has SessionID/Resumable/etc. to build a run from).
// It is the only entry point that puts a job into Running, and it names
// the job explicitly rather than picking one: nothing here selects, loops,
// or retries on its own. See internal/dispatch for the "dispatch this one
// job now" primitive this backs.
func (q *Queue) MarkRunning(ctx context.Context, id string) (store.Job, error) {
	j, err := q.store.GetJob(ctx, id)
	if err != nil {
		return store.Job{}, err
	}
	if j.Status != store.StatusQueued && j.Status != store.StatusPausedBudget {
		return store.Job{}, fmt.Errorf("%w: job %s is %s", ErrNotRunnable, id, j.Status)
	}
	if err := q.store.UpdateStatus(ctx, id, store.StatusRunning, ""); err != nil {
		return store.Job{}, err
	}
	return j, nil
}

// FinishOutcome is the terminal state DispatchOne/RunJob record after a
// dispatch. Result is the runner's final text output (see store.Job.Result's
// doc comment); it's set regardless of whether Status is Succeeded or Failed.
type FinishOutcome struct {
	Status        store.Status
	FailureReason string
	SessionID     string
	Result        string
}

// Finish records the outcome of a dispatch: the job's terminal status
// (typically Succeeded or Failed), an optional failure reason, and — when
// non-empty — the session ID and result text the run produced, so a later
// --resume can continue it even after a failed or budget-capped run, and
// so the job's output is retrievable afterward.
func (q *Queue) Finish(ctx context.Context, id string, outcome FinishOutcome) error {
	if outcome.SessionID != "" {
		if err := q.store.UpdateSessionID(ctx, id, outcome.SessionID); err != nil {
			return err
		}
	}
	if outcome.Result != "" {
		if err := q.store.UpdateResult(ctx, id, outcome.Result); err != nil {
			return err
		}
	}
	return q.store.UpdateStatus(ctx, id, outcome.Status, outcome.FailureReason)
}

// DeferOversized transitions a non-terminal job to DeferredOversized,
// recording why it couldn't be fit into remaining headroom (REQUIREMENTS.md
// §6.4 step 4). Like Cancel, deferring an already-terminal job is an error
// rather than a silent no-op.
func (q *Queue) DeferOversized(ctx context.Context, id, reason string) error {
	j, err := q.store.GetJob(ctx, id)
	if err != nil {
		return err
	}
	if j.Status.Terminal() {
		return fmt.Errorf("%w: job %s is already %s", ErrAlreadyTerminal, id, j.Status)
	}
	return q.store.UpdateStatus(ctx, id, store.StatusDeferredOversized, reason)
}

// resolveDeps fetches every dependency job and errors naming any ID that
// doesn't exist, rather than silently treating a typo'd dependency ID as
// "not yet satisfied" forever.
func (q *Queue) resolveDeps(ctx context.Context, ids []string) ([]store.Job, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	deps, err := q.store.GetJobs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("resolving dependencies: %w", err)
	}
	if len(deps) == len(ids) {
		return deps, nil
	}

	found := make(map[string]bool, len(deps))
	for _, d := range deps {
		found[d.ID] = true
	}
	var missing []string
	for _, id := range ids {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrDependencyNotFound, strings.Join(missing, ", "))
}

// computeStatus decides a job's status from its (already-resolved)
// dependencies: Queued if there are none or all succeeded, Cancelled if any
// reached a terminal non-success state, otherwise Blocked.
func computeStatus(deps []store.Job) (store.Status, string) {
	if len(deps) == 0 {
		return store.StatusQueued, ""
	}

	var failedIDs []string
	allSucceeded := true
	for _, d := range deps {
		if d.Status != store.StatusSucceeded {
			allSucceeded = false
		}
		if d.Status.Terminal() && d.Status != store.StatusSucceeded {
			failedIDs = append(failedIDs, d.ID)
		}
	}

	if len(failedIDs) > 0 {
		return store.StatusCancelled, fmt.Sprintf("dependency failed or was cancelled: %s", strings.Join(failedIDs, ", "))
	}
	if allSucceeded {
		return store.StatusQueued, ""
	}
	return store.StatusBlocked, ""
}
