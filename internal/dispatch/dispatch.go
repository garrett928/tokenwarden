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

	"tokenwarden/internal/budget"
	"tokenwarden/internal/queue"
	"tokenwarden/internal/runner"
	"tokenwarden/internal/store"
)

// Dispatcher runs one named job at a time against a Queue and a Runner,
// recording its outcome onto a Ledger.
type Dispatcher struct {
	queue  *queue.Queue
	runner *runner.Runner
	ledger *budget.Ledger
}

// New builds a Dispatcher backed by q, r, and l.
func New(q *queue.Queue, r *runner.Runner, l *budget.Ledger) *Dispatcher {
	return &Dispatcher{queue: q, runner: r, ledger: l}
}

// DispatchOne runs the named job right now: it marks the job Running,
// invokes the runner, and records Succeeded or Failed based on the
// outcome. Returns whatever error queue.MarkRunning produces (notably
// queue.ErrNotRunnable) without ever starting the subprocess.
func (d *Dispatcher) DispatchOne(ctx context.Context, jobID string) error {
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
	result, runErr := d.runner.Run(ctx, job, runner.RunOptions{})
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
	if result.IsError {
		finishErr = d.queue.Finish(ctx, job.ID, queue.FinishOutcome{Status: store.StatusFailed, FailureReason: result.Result, SessionID: result.SessionID, Result: result.Result})
	} else {
		finishErr = d.queue.Finish(ctx, job.ID, queue.FinishOutcome{Status: store.StatusSucceeded, SessionID: result.SessionID, Result: result.Result})
	}

	return errors.Join(recordErr, finishErr)
}
