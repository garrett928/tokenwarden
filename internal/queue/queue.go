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

// ErrNoSteps is returned by PromoteSteps when the job declares no Steps to
// promote — §6.4 strategy 2 doesn't apply to it, and inventing split points
// is strategy 3's job, not this one's.
var ErrNoSteps = errors.New("job has no steps to promote")

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
		// A promoted step chain (§6.4 strategy 2) shares one session across
		// its children: the step that just succeeded hands its session to the
		// next one, so the next dispatch continues the same conversation via
		// --resume rather than starting cold. Written before the status
		// change so a child is never observably Queued without the session it
		// is supposed to inherit.
		if status == store.StatusQueued && b.ParentJobID != "" && len(deps) == 1 && deps[0].SessionID != "" {
			if err := q.store.UpdateSessionID(ctx, b.ID, deps[0].SessionID); err != nil {
				errs = append(errs, fmt.Errorf("job %s: inheriting session from %s: %w", b.ID, deps[0].ID, err))
				continue
			}
		}
		if err := q.store.UpdateStatus(ctx, b.ID, status, reason); err != nil {
			errs = append(errs, fmt.Errorf("job %s: %w", b.ID, err))
			continue
		}
		promoted++
	}

	return promoted, errors.Join(errs...)
}

// Candidates returns every Queued or PausedBudget job whose EarliestAt (if
// any) has passed as of now, in the store's existing priority order
// (highest first, then oldest-created first). PausedBudget jobs are
// included alongside Queued ones because MarkRunning already accepts both
// as a dispatch source (a budget-capped job resuming next window is just as
// runnable as a fresh one) — see §6.4 strategy 1. The scheduler walks this
// full list, rather than just the first entry, so a top candidate that
// doesn't fit remaining headroom can be deferred in favor of the next-best
// one (§6.2 step 7).
func (q *Queue) Candidates(ctx context.Context, now time.Time) ([]store.Job, error) {
	jobs, err := q.store.ListJobs(ctx, store.ListFilter{Statuses: []store.Status{store.StatusQueued, store.StatusPausedBudget}})
	if err != nil {
		return nil, fmt.Errorf("listing queued jobs: %w", err)
	}

	var runnable []store.Job
	for _, j := range jobs {
		if j.EarliestAt != nil && j.EarliestAt.After(now) {
			continue
		}
		runnable = append(runnable, j)
	}
	return runnable, nil
}

// NextRunnable returns Candidates' first entry — the single
// highest-priority runnable job — for callers (the API's manual dispatch
// trigger, tests) that don't need the full list. ok is false when nothing
// is eligible yet.
func (q *Queue) NextRunnable(ctx context.Context, now time.Time) (job store.Job, ok bool, err error) {
	candidates, err := q.Candidates(ctx, now)
	if err != nil {
		return store.Job{}, false, err
	}
	if len(candidates) == 0 {
		return store.Job{}, false, nil
	}
	return candidates[0], true, nil
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

// CapBudget sets a job's MaxBudgetUSD so its next dispatch stops at usdCap
// and (when the job is Resumable) can be continued later via --resume —
// REQUIREMENTS.md §6.4 strategy 1, budget-capped continuation. Like Cancel
// and DeferOversized, capping an already-terminal job is an error rather
// than a silent no-op.
func (q *Queue) CapBudget(ctx context.Context, id string, usdCap float64) error {
	j, err := q.store.GetJob(ctx, id)
	if err != nil {
		return err
	}
	if j.Status.Terminal() {
		return fmt.Errorf("%w: job %s is already %s", ErrAlreadyTerminal, id, j.Status)
	}
	return q.store.UpdateJobMaxBudgetUSD(ctx, id, &usdCap)
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

// PromoteSteps implements REQUIREMENTS.md §6.4 strategy 2: it turns job's
// declared Steps into a chain of child jobs — child 0 runnable immediately,
// each subsequent child DependsOn the previous one, so only one step is
// ever runnable at a time and PromoteReady's existing dependency-sweep
// naturally advances the chain as each step succeeds. Every child inherits
// the parent's execution settings *and* its safety envelope — kind, model,
// effort, workspace, priority, tool allowlist, permission mode, extra
// directories, worktree flag, per-job budget cap, attachments, JSON schema
// and deadline — because an empty AllowedTools or PermissionMode is not
// "inherit the parent's": internal/runner/safety.go reads it as "apply the
// kind's default", which for a code job adds Bash/Write and for a freeform
// job auto-approves permissions (FR-SAFE-3). Dropping those fields would
// silently widen what an unattended step may do relative to what the user
// queued, and dropping MaxBudgetUSD would defeat FR-SAFE-2's per-job spend
// cap on every child. Children also inherit the parent's own Resumable
// setting, since reaching this strategy already means the user marked the
// whole job as unsafe to interrupt mid-run; that guarantee carries over to
// each step. Each child is tagged with ParentJobID so it's traceable back
// to the promoted parent, and the first child inherits the parent's
// SessionID when it has one, so a parent that already paid for context
// (e.g. one paused at a budget cap) continues that conversation rather than
// throwing it away — §6.4's "children share the parent's session".
//
// The parent itself is marked StatusPromoted — its own work now lives
// entirely in the children — recording which child IDs it became. Because
// Promoted is terminal-but-not-succeeded, any job that was Blocked on the
// parent is repointed at the chain's last child first: otherwise the next
// PromoteReady sweep would read the parent as a dead dependency and cancel
// those dependents, even though the work is proceeding normally.
//
// PromoteSteps is idempotent by ParentJobID: if children already exist for
// this parent it creates none, and only ensures the parent is recorded as
// Promoted (self-healing a previous call that created children but failed
// before that final status write). Without that, a partial failure would
// have the scheduler re-promote the same parent every tick, spending real
// tokens on a fresh set of duplicate children each time.
func (q *Queue) PromoteSteps(ctx context.Context, id string) ([]store.Job, error) {
	job, err := q.store.GetJob(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(job.Steps) == 0 {
		return nil, fmt.Errorf("%w: job %s", ErrNoSteps, id)
	}

	// Checked before the terminal guard rather than after it, because a
	// successful promotion leaves the parent terminal (Promoted): a repeat
	// call has to land here, not in ErrAlreadyTerminal.
	existing, err := q.store.ListJobs(ctx, store.ListFilter{ParentJobID: id})
	if err != nil {
		return nil, fmt.Errorf("listing existing children of job %s: %w", id, err)
	}
	if len(existing) > 0 {
		existing = orderChain(existing)
		if job.Status != store.StatusPromoted {
			if err := q.store.UpdateStatus(ctx, id, store.StatusPromoted, promotionReason(existing)); err != nil {
				return nil, err
			}
		}
		return existing, nil
	}

	if job.Status.Terminal() {
		return nil, fmt.Errorf("%w: job %s is already %s", ErrAlreadyTerminal, id, job.Status)
	}

	children := make([]store.Job, 0, len(job.Steps))
	for i, step := range job.Steps {
		child := store.Job{
			Kind:             job.Kind,
			Prompt:           step,
			Workspace:        job.Workspace,
			Model:            job.Model,
			Effort:           job.Effort,
			Priority:         job.Priority,
			ParentJobID:      job.ID,
			Resumable:        job.Resumable,
			AllowedTools:     job.AllowedTools,
			PermissionMode:   job.PermissionMode,
			AddDirs:          job.AddDirs,
			FreeformWorktree: job.FreeformWorktree,
			MaxBudgetUSD:     job.MaxBudgetUSD,
			Attachments:      job.Attachments,
			JSONSchema:       job.JSONSchema,
			DeadlineAt:       job.DeadlineAt,
		}
		if i == 0 {
			// runner.BuildArgs decides --resume purely on SessionID being
			// non-empty, so handing the parent's session to the head of the
			// chain is safe regardless of the child's own Resumable setting.
			child.SessionID = job.SessionID
		} else {
			child.DependsOn = []string{children[i-1].ID}
		}

		// Created one at a time and in order, so each child's DependsOn names
		// a job that already exists. A failure partway through leaves the
		// children created so far in place rather than rolling back: nothing
		// in this codebase spans a transaction across store calls, and the
		// idempotency check above makes the retry pick up where this left off
		// instead of duplicating the chain.
		created, err := q.Enqueue(ctx, child)
		if err != nil {
			return nil, fmt.Errorf("promoting step %d of job %s: %w", i, id, err)
		}
		children = append(children, created)
	}

	// Rewiring failures are collected rather than fatal: the children are
	// created and correct either way, and the parent still needs to reach
	// StatusPromoted so a retry doesn't re-promote it.
	var errs []error
	if err := q.rewireDependents(ctx, job.ID, children[len(children)-1].ID); err != nil {
		errs = append(errs, err)
	}
	if err := q.store.UpdateStatus(ctx, id, store.StatusPromoted, promotionReason(children)); err != nil {
		errs = append(errs, err)
		return nil, errors.Join(errs...)
	}
	return children, errors.Join(errs...)
}

// rewireDependents repoints every Blocked job that depends on parentID at
// lastChildID instead — the chain's final unit of work, which is what such
// a dependent actually meant to wait for. See PromoteSteps for why this is
// necessary at all (Promoted is terminal and not Succeeded, so leaving the
// dependency in place would have computeStatus cancel these jobs). Errors
// are joined rather than aborting the sweep, exactly like PromoteReady.
func (q *Queue) rewireDependents(ctx context.Context, parentID, lastChildID string) error {
	blocked, err := q.store.ListJobs(ctx, store.ListFilter{Statuses: []store.Status{store.StatusBlocked}})
	if err != nil {
		return fmt.Errorf("listing blocked dependents of job %s: %w", parentID, err)
	}

	var errs []error
	for _, b := range blocked {
		rewritten := make([]string, 0, len(b.DependsOn))
		var found bool
		for _, dep := range b.DependsOn {
			if dep == parentID {
				dep = lastChildID
				found = true
			}
			rewritten = append(rewritten, dep)
		}
		if !found {
			continue
		}
		if err := q.store.UpdateDependsOn(ctx, b.ID, rewritten); err != nil {
			errs = append(errs, fmt.Errorf("job %s: repointing dependency %s at %s: %w", b.ID, parentID, lastChildID, err))
		}
	}
	return errors.Join(errs...)
}

// promotionReason is the parent's FailureReason text, naming the children
// it became so the promotion stays traceable from the parent alone.
func promotionReason(children []store.Job) string {
	ids := make([]string, 0, len(children))
	for _, c := range children {
		ids = append(ids, c.ID)
	}
	return fmt.Sprintf("promoted into %d step(s): %s", len(ids), strings.Join(ids, ", "))
}

// orderChain sorts a promoted parent's children back into chain order (head
// first, each subsequent child depending on the one before it). ListJobs
// orders by priority then created_at, and a promotion's children share a
// priority and are created within the same one-second created_at tick, so
// storage order says nothing about the chain — the DependsOn links do. A
// set of children that doesn't form a single chain is handed back untouched
// rather than guessed at.
func orderChain(children []store.Job) []store.Job {
	ids := make(map[string]bool, len(children))
	for _, c := range children {
		ids[c.ID] = true
	}

	successor := make(map[string]store.Job, len(children))
	var head *store.Job
	for i, c := range children {
		var prev string
		for _, dep := range c.DependsOn {
			if ids[dep] {
				prev = dep
				break
			}
		}
		if prev == "" {
			if head != nil {
				return children // more than one head: not a single chain
			}
			head = &children[i]
			continue
		}
		successor[prev] = c
	}
	if head == nil {
		return children
	}

	ordered := make([]store.Job, 0, len(children))
	cur := *head
	for range children {
		ordered = append(ordered, cur)
		next, ok := successor[cur.ID]
		if !ok {
			break
		}
		cur = next
	}
	if len(ordered) != len(children) {
		return children
	}
	return ordered
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
//
// StatusPromoted is deliberately not special-cased here even though it is
// terminal-and-not-succeeded: a promoted dependency's work is proceeding in
// its children, so cancelling its dependents would be wrong. PromoteSteps
// handles it instead, by repointing every Blocked dependent at the chain's
// last child at promotion time — so by the time any sweep runs,
// computeStatus should never see a Promoted dependency at all. One that
// does show up (a job enqueued against an already-promoted parent) is
// genuinely un-runnable as written and cancelling it is the right call.
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
