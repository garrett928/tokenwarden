// Package dispatch is the seam between a queued job and internal/runner:
// DispatchOne runs exactly the job named by its caller, once, and records
// the outcome. It is deliberately not a scheduler — nothing here selects
// which job to run, retries on failure, or repeats on a timer. A future
// internal/scheduler is expected to call this same primitive from its own
// budget-aware policy loop rather than reinvent it.
package dispatch

import (
	"context"
	"fmt"

	"tokenwarden/internal/queue"
	"tokenwarden/internal/runner"
	"tokenwarden/internal/store"
)

// Dispatcher runs one named job at a time against a Queue and a Runner.
type Dispatcher struct {
	queue  *queue.Queue
	runner *runner.Runner
}

// New builds a Dispatcher backed by q and r.
func New(q *queue.Queue, r *runner.Runner) *Dispatcher {
	return &Dispatcher{queue: q, runner: r}
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
// queue.MarkRunning) and records the outcome. Split out from DispatchOne so
// a caller that needs to return control to its own caller before the run
// finishes — the API's dispatch handler, which marks the job running
// synchronously but runs it in a goroutine so the HTTP request doesn't
// block on a potentially long job — can do the state transition and the
// run as two separate steps.
func (d *Dispatcher) RunJob(ctx context.Context, job store.Job) error {
	result, runErr := d.runner.Run(ctx, job, runner.RunOptions{})
	if runErr != nil {
		return d.queue.Finish(ctx, job.ID, store.StatusFailed, runErr.Error(), "")
	}
	if result.IsError {
		return d.queue.Finish(ctx, job.ID, store.StatusFailed, result.Result, result.SessionID)
	}
	return d.queue.Finish(ctx, job.ID, store.StatusSucceeded, "", result.SessionID)
}
