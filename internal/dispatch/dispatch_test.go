package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tokenwarden/internal/budget"
	"tokenwarden/internal/queue"
	"tokenwarden/internal/runner"
	"tokenwarden/internal/store"
)

// fakeClaudeBin is built once from internal/runner's fixture harness — see
// that package's doc comments for why a fake binary exists at all
// (CLAUDE.md/NFR-TEST-1: never shell out to the real claude CLI in tests).
var fakeClaudeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fakeclaude")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating temp dir for fakeclaude:", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	out := filepath.Join(dir, "fakeclaude")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	build := exec.Command("go", "build", "-o", out, "../runner/testdata/fakeclaude")
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building fakeclaude: %v\n%s", err, output)
		os.Exit(1)
	}
	fakeClaudeBin = out

	os.Exit(m.Run())
}

func newTestDispatcher(t *testing.T) *Dispatcher {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	q := queue.New(s)
	r := runner.New(fakeClaudeBin)
	l := budget.New(s)
	return New(q, r, l)
}

func TestDispatchOne_Success(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	d := newTestDispatcher(t)
	ctx := context.Background()

	created, err := d.queue.Enqueue(ctx, store.Job{Kind: store.JobKindResearch, Prompt: "say pong"})
	if err != nil {
		t.Fatal(err)
	}

	if err := d.DispatchOne(ctx, created.ID); err != nil {
		t.Fatalf("DispatchOne() error: %v", err)
	}

	got, err := d.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusSucceeded {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusSucceeded)
	}
	if got.SessionID == "" {
		t.Error("SessionID is empty, want the fixture's session id recorded")
	}
	if got.Result != "pong" {
		t.Errorf("Result = %q, want %q (the happy_path fixture's result text)", got.Result, "pong")
	}

	totals, err := d.ledger.FiveHourTotal(ctx, time.Now())
	if err != nil {
		t.Fatalf("FiveHourTotal() error: %v", err)
	}
	if totals.EntryCount == 0 {
		t.Error("ledger has no entries after a successful dispatch, want the happy_path fixture's usage recorded")
	}
	if totals.CostUSD != 0.0230845 {
		t.Errorf("ledger CostUSD = %v, want the fixture's total_cost_usd 0.0230845", totals.CostUSD)
	}
}

func TestDispatchOne_NotRunnable(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	d := newTestDispatcher(t)
	ctx := context.Background()

	created, err := d.queue.Enqueue(ctx, store.Job{Kind: store.JobKindResearch, Prompt: "say pong"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.queue.Cancel(ctx, created.ID); err != nil {
		t.Fatal(err)
	}

	if err := d.DispatchOne(ctx, created.ID); !errors.Is(err, queue.ErrNotRunnable) {
		t.Errorf("DispatchOne() error = %v, want queue.ErrNotRunnable", err)
	}

	got, err := d.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusCancelled {
		t.Errorf("Status changed to %q after a rejected dispatch, want unchanged %q", got.Status, store.StatusCancelled)
	}
}

func TestDispatchOne_RunnerFailure(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "no_result")
	d := newTestDispatcher(t)
	ctx := context.Background()

	created, err := d.queue.Enqueue(ctx, store.Job{Kind: store.JobKindResearch, Prompt: "say pong"})
	if err != nil {
		t.Fatal(err)
	}

	if err := d.DispatchOne(ctx, created.ID); err != nil {
		t.Fatalf("DispatchOne() error: %v, want nil (the runner failure is recorded onto the job, not returned)", err)
	}

	got, err := d.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusFailed {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusFailed)
	}
	if got.FailureReason == "" {
		t.Error("FailureReason is empty, want the runner's error recorded")
	}
}

func TestDispatchOne_ErrorResult(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "error_exit")
	d := newTestDispatcher(t)
	ctx := context.Background()

	created, err := d.queue.Enqueue(ctx, store.Job{Kind: store.JobKindResearch, Prompt: "say pong"})
	if err != nil {
		t.Fatal(err)
	}

	if err := d.DispatchOne(ctx, created.ID); err != nil {
		t.Fatalf("DispatchOne() error: %v, want nil", err)
	}

	got, err := d.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusFailed {
		t.Errorf("Status = %q, want %q (claude reported is_error)", got.Status, store.StatusFailed)
	}
}

func TestHalt_CancelsInFlightRun(t *testing.T) {
	d := newTestDispatcher(t)

	var canceled bool
	d.mu.Lock()
	d.running["job_x"] = func() { canceled = true }
	d.mu.Unlock()

	d.Halt()

	if !canceled {
		t.Error("Halt() did not call the registered cancel func for an in-flight run")
	}
	if !d.Halted() {
		t.Error("Halted() = false after Halt()")
	}
}

func TestDispatchOne_FailsFastWhenHalted(t *testing.T) {
	d := newTestDispatcher(t)
	ctx := context.Background()

	created, err := d.queue.Enqueue(ctx, store.Job{Kind: store.JobKindResearch, Prompt: "say pong"})
	if err != nil {
		t.Fatal(err)
	}

	d.Halt()

	if err := d.DispatchOne(ctx, created.ID); !errors.Is(err, ErrHalted) {
		t.Errorf("DispatchOne() error = %v, want ErrHalted", err)
	}

	got, err := d.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusQueued {
		t.Errorf("Status = %q, want %q (should not have transitioned to Running/Failed)", got.Status, store.StatusQueued)
	}
}

func TestRunJob_FailsWhenHalted(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	d := newTestDispatcher(t)
	ctx := context.Background()

	created, err := d.queue.Enqueue(ctx, store.Job{Kind: store.JobKindResearch, Prompt: "say pong"})
	if err != nil {
		t.Fatal(err)
	}

	job, err := d.queue.MarkRunning(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}

	d.Halt()

	if err := d.RunJob(ctx, job); err != nil {
		t.Fatalf("RunJob() error: %v, want nil (the failure is recorded onto the job, not returned)", err)
	}

	got, err := d.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusFailed {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusFailed)
	}
	if !strings.Contains(got.FailureReason, "kill switch") {
		t.Errorf("FailureReason = %q, want it to contain 'kill switch'", got.FailureReason)
	}
}

func TestResume_AllowsDispatchAgain(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	d := newTestDispatcher(t)
	ctx := context.Background()

	created, err := d.queue.Enqueue(ctx, store.Job{Kind: store.JobKindResearch, Prompt: "say pong"})
	if err != nil {
		t.Fatal(err)
	}

	d.Halt()
	d.Resume()

	if err := d.DispatchOne(ctx, created.ID); err != nil {
		t.Fatalf("DispatchOne() after Resume() error: %v", err)
	}

	got, err := d.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != store.StatusSucceeded {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusSucceeded)
	}
}
