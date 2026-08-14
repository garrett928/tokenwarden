// Package dispatch is the seam between a queued job and internal/runner:
// DispatchOne runs exactly the job named by its caller, once, and records
// the outcome. It is deliberately not a scheduler — nothing here selects
// which job to run, retries on failure, or repeats on a timer. A future
// internal/scheduler is expected to call this same primitive from its own
// budget-aware policy loop rather than reinvent it.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"tokenwarden/internal/budget"
	"tokenwarden/internal/queue"
	"tokenwarden/internal/runner"
	"tokenwarden/internal/store"
)

// ErrHalted is returned when a dispatch is attempted while the kill
// switch (FR-SAFE-4) is active.
var ErrHalted = errors.New("dispatch: kill switch is active")

// Dispatcher runs one named job at a time against a Queue and a Runner,
// recording its outcome onto a Ledger. It also holds the kill switch
// (FR-SAFE-4): Halt marks the dispatcher halted and cancels every
// in-flight run's context, which terminates its claude subprocess (see
// internal/runner.Runner.Run's use of exec.CommandContext on that same
// ctx). The halted flag is in-memory only — it does not survive a daemon
// restart.
type Dispatcher struct {
	queue  *queue.Queue
	runner *runner.Runner
	ledger *budget.Ledger

	mu      sync.Mutex
	halted  bool
	running map[string]context.CancelFunc
}

// New builds a Dispatcher backed by q, r, and l.
func New(q *queue.Queue, r *runner.Runner, l *budget.Ledger) *Dispatcher {
	return &Dispatcher{queue: q, runner: r, ledger: l, running: make(map[string]context.CancelFunc)}
}

// Halt activates the kill switch (FR-SAFE-4): every future DispatchOne
// or RunJob call fails immediately with ErrHalted without starting a
// subprocess, and every currently in-flight run's context is cancelled,
// terminating its claude subprocess. Idempotent — calling it again while
// already halted is a no-op beyond re-cancelling (harmless).
func (d *Dispatcher) Halt() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.halted = true
	for _, cancel := range d.running {
		cancel()
	}
}

// Resume deactivates the kill switch so future dispatches are allowed
// again. It does not restart or retry anything Halt terminated — those
// jobs were already recorded Failed with ErrHalted's message.
func (d *Dispatcher) Resume() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.halted = false
}

// Halted reports whether the kill switch is currently active.
func (d *Dispatcher) Halted() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.halted
}

// DispatchOne runs the named job right now: it marks the job Running,
// invokes the runner, and records Succeeded or Failed based on the
// outcome. Returns whatever error queue.MarkRunning produces (notably
// queue.ErrNotRunnable) without ever starting the subprocess.
func (d *Dispatcher) DispatchOne(ctx context.Context, jobID string) error {
	if d.Halted() {
		return ErrHalted
	}
	job, err := d.queue.MarkRunning(ctx, jobID)
	if err != nil {
		return fmt.Errorf("marking job %s running: %w", jobID, err)
	}
	return d.RunJob(ctx, job)
}

// RunJob runs a job that has already been marked Running (see
// queue.MarkRunning), appends its usage to the ledger, and records the
// outcome. Split out from DispatchOne so a caller that needs to return
// control to its own caller before the run finishes — the API's dispatch
// handler, which marks the job running synchronously but runs it in a
// goroutine so the HTTP request doesn't block on a potentially long job —
// can do the state transition and the run as two separate steps.
func (d *Dispatcher) RunJob(ctx context.Context, job store.Job) error {
	runCtx, cancel := context.WithCancel(ctx)

	d.mu.Lock()
	if d.halted {
		d.mu.Unlock()
		cancel()
		return d.queue.Finish(ctx, job.ID, queue.FinishOutcome{Status: store.StatusFailed, FailureReason: ErrHalted.Error()})
	}
	d.running[job.ID] = cancel
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		delete(d.running, job.ID)
		d.mu.Unlock()
		cancel()
	}()

	result, runErr := d.runner.Run(runCtx, job, runner.RunOptions{})
	if runErr != nil {
		// No Result was ever produced, so there's nothing for the ledger to
		// record (REQUIREMENTS.md §6.2 step 8: "append to ledger on
		// completion" — a run that never completed has no usage to append).
		return d.queue.Finish(ctx, job.ID, queue.FinishOutcome{Status: store.StatusFailed, FailureReason: runErr.Error()})
	}

	// A ledger-write failure shouldn't leave the job stuck in Running — the
	// dispatch itself succeeded or failed independently of whether we
	// managed to record it — but it must not be silently swallowed either.
	var recordErr error
	if err := d.ledger.RecordResult(ctx, job.ID, result); err != nil {
		recordErr = fmt.Errorf("recording usage for job %s: %w", job.ID, err)
	}

	var finishErr error
	switch {
	case isBudgetCutoff(job, result):
		// --max-budget-usd stopped the run mid-task rather than the model
		// finishing on its own — StatusPausedBudget (not Failed) so
		// queue.MarkRunning's existing "Queued or PausedBudget" acceptance
		// lets a later dispatch --resume it (§6.4 strategy 1), regardless of
		// whether the CLI happened to also report IsError for the cutoff.
		finishErr = d.queue.Finish(ctx, job.ID, queue.FinishOutcome{Status: store.StatusPausedBudget, SessionID: result.SessionID, Result: result.Result})
	case result.IsError:
		finishErr = d.queue.Finish(ctx, job.ID, queue.FinishOutcome{Status: store.StatusFailed, FailureReason: result.Result, SessionID: result.SessionID, Result: result.Result})
	default:
		finishErr = d.queue.Finish(ctx, job.ID, queue.FinishOutcome{Status: store.StatusSucceeded, SessionID: result.SessionID, Result: result.Result})
	}

	return errors.Join(recordErr, finishErr)
}

// isBudgetCutoff reports whether result looks like a run --max-budget-usd
// cut short rather than one that finished (successfully or not) on its
// own. There is no verified stop_reason value for a budget cutoff (unlike
// end_turn — SPIKE-001 never exercised --max-budget-usd), so this is
// deliberately based on data BuildArgs and the runner already produce
// rather than a guessed enum string: a capped run cannot spend more than
// its cap, so reaching or exceeding it with a resumable session in hand is
// the cutoff signature. A run that finishes naturally exactly at the cap
// is indistinguishable from this and is treated as a cutoff too — the
// worse case is an unnecessary --resume, not lost work.
func isBudgetCutoff(job store.Job, result runner.Result) bool {
	return job.MaxBudgetUSD != nil && result.SessionID != "" && result.TotalCostUSD >= *job.MaxBudgetUSD
}
