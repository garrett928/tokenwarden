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

func TestDispatchOne_BudgetCutoff_PausesForResume(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "budget_capped")
	d := newTestDispatcher(t)
	ctx := context.Background()

	budgetCap := 0.05 // fixture's total_cost_usd reaches this exactly
	created, err := d.queue.Enqueue(ctx, store.Job{Kind: store.JobKindResearch, Prompt: "do a big thing", MaxBudgetUSD: &budgetCap, Resumable: true})
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
	if got.Status != store.StatusPausedBudget {
		t.Errorf("Status = %q, want %q (cost reached the cap)", got.Status, store.StatusPausedBudget)
	}
	if got.SessionID == "" {
		t.Error("SessionID is empty, want the fixture's session id recorded so a later dispatch can --resume")
	}
}

// TestRunJob_RefusesResumeOnceMaxBudgetExhausted is the regression test for
// a real incident: a Resumable job with a cap too tight to ever finish in
// one dispatch was resumed automatically 151 times over 23 hours (the
// scheduler's PausedBudgetRetryCooldown throttled the *rate*, correctly,
// but nothing stopped it from being retried forever) — $359 in cumulative
// cost against a two-cent cap, because --max-budget-usd is enforced by the
// CLI per invocation, not across a job's lifetime, and each individual
// attempt's own cost looked unremarkable to isBudgetCutoff in isolation.
func TestRunJob_RefusesResumeOnceMaxBudgetExhausted(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "budget_capped")
	d := newTestDispatcher(t)
	ctx := context.Background()

	// The fixture's total_cost_usd is 0.05 — a 0.02 cap means the very
	// first dispatch already exceeds it, exactly like the real incident
	// (a cap far below the cost of even one real attempt). UserMaxBudgetUSD
	// is what the new guard actually checks (see dispatch.go) — set it
	// alongside MaxBudgetUSD exactly as CreateJobRequest.toJob() would for
	// a job the user capped directly at creation, with no scheduler
	// involvement at all.
	budgetCap := 0.02
	created, err := d.queue.Enqueue(ctx, store.Job{Kind: store.JobKindResearch, Prompt: "do a big thing", MaxBudgetUSD: &budgetCap, UserMaxBudgetUSD: &budgetCap, Resumable: true})
	if err != nil {
		t.Fatal(err)
	}

	if err := d.DispatchOne(ctx, created.ID); err != nil {
		t.Fatalf("first DispatchOne() error: %v", err)
	}
	afterFirst, err := d.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterFirst.Status != store.StatusPausedBudget {
		t.Fatalf("Status after first dispatch = %q, want %q", afterFirst.Status, store.StatusPausedBudget)
	}
	spentAfterFirst, err := d.ledger.JobCumulativeCost(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if spentAfterFirst != 0.05 {
		t.Fatalf("cumulative spend after first dispatch = %v, want the fixture's 0.05", spentAfterFirst)
	}

	// Second attempt (what the scheduler's cooldown-expired resume, or a
	// manual `queue dispatch`, would do next): must be refused BEFORE
	// spawning the runner again, not merely re-capped or re-attempted.
	if err := d.DispatchOne(ctx, created.ID); err != nil {
		t.Fatalf("second DispatchOne() error: %v", err)
	}
	afterSecond, err := d.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterSecond.Status != store.StatusFailed {
		t.Errorf("Status after second dispatch = %q, want %q (exhausted budget, refused to resume)", afterSecond.Status, store.StatusFailed)
	}
	if !strings.Contains(afterSecond.FailureReason, "exhausted MaxBudgetUSD") {
		t.Errorf("FailureReason = %q, want it to explain the exhausted cumulative cap", afterSecond.FailureReason)
	}

	// The real proof: cumulative cost must be UNCHANGED from after the
	// first dispatch — if the runner had actually run a second time (even
	// briefly), the budget_capped fixture would add another 0.05.
	spentAfterSecond, err := d.ledger.JobCumulativeCost(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if spentAfterSecond != spentAfterFirst {
		t.Errorf("cumulative spend after second dispatch = %v, want unchanged %v (runner must not have run again)", spentAfterSecond, spentAfterFirst)
	}
}

// TestRunJob_SchedulerCappedJob_NotSubjectToLifetimeCheck is the other half
// of the regression test above: a review of the cumulative-spend guard
// found that, checked against plain MaxBudgetUSD, it would break §6.4
// strategy 1 (budget-capped continuation) for any job the *scheduler*
// capped rather than the user — queue.CapBudget writes MaxBudgetUSD, and
// that value can go stale (a job capped to fit one window's headroom that
// later gets enough headroom to finish outright is never re-capped, so the
// old smaller value would otherwise sit there looking like an
// already-exhausted lifetime budget). This confirms a job with no
// UserMaxBudgetUSD (never capped by the user, only by the scheduler) is not
// subject to the lifetime check at all, even though cumulative cost already
// exceeds the stale scheduler-set MaxBudgetUSD.
func TestRunJob_SchedulerCappedJob_NotSubjectToLifetimeCheck(t *testing.T) {
	d := newTestDispatcher(t)
	ctx := context.Background()

	created, err := d.queue.Enqueue(ctx, store.Job{Kind: store.JobKindResearch, Prompt: "do a big thing", Resumable: true})
	if err != nil {
		t.Fatal(err)
	}

	// Simulates what scheduler.FitJob's FitCapBudget verdict does: cap an
	// uncapped-by-the-user job to fit remaining window headroom.
	if err := d.queue.CapBudget(ctx, created.ID, 0.04); err != nil {
		t.Fatalf("CapBudget() error: %v", err)
	}

	t.Setenv("FAKECLAUDE_FIXTURE", "budget_capped") // fixture cost 0.05 exceeds the 0.04 cap
	if err := d.DispatchOne(ctx, created.ID); err != nil {
		t.Fatalf("first DispatchOne() error: %v", err)
	}
	afterFirst, err := d.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterFirst.Status != store.StatusPausedBudget {
		t.Fatalf("Status after first dispatch = %q, want %q", afterFirst.Status, store.StatusPausedBudget)
	}
	if afterFirst.UserMaxBudgetUSD != nil {
		t.Fatal("UserMaxBudgetUSD is set after CapBudget, want nil (CapBudget must never touch it)")
	}

	// A later tick decides this job now fits without needing to be re-capped
	// at all (FitDispatch, not FitCapBudget) — the stale 0.04 scheduler
	// value is still sitting on the job, unrelated to its real chances now.
	// Cumulative spend (0.05) already exceeds it, but this must still be
	// allowed to actually finish, since the cap was never the user's own
	// lifetime intent.
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	if err := d.DispatchOne(ctx, created.ID); err != nil {
		t.Fatalf("second DispatchOne() error: %v", err)
	}
	final, err := d.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != store.StatusSucceeded {
		t.Errorf("Status after legitimate resume = %q, want %q — a scheduler-set cap must not block it just because cumulative spend exceeds the stale value", final.Status, store.StatusSucceeded)
	}
}

// TestRunJob_UserCappedJobLaterSchedulerCapped_StillEnforcesLifetimeCap
// covers the case the reviewer of the two tests above flagged as missing:
// a job the USER capped, which the scheduler THEN also caps down further to
// fit a tight window (§6.4 strategy 1 on top of a real user cap). The
// user's lifetime intent must still be enforced — CapBudget must never
// raise the effective cap above it, and once cumulative spend reaches the
// user's real cap, the job must stop being resumed regardless of what the
// scheduler's own per-window value says.
func TestRunJob_UserCappedJobLaterSchedulerCapped_StillEnforcesLifetimeCap(t *testing.T) {
	d := newTestDispatcher(t)
	ctx := context.Background()

	userCap := 0.03
	created, err := d.queue.Enqueue(ctx, store.Job{Kind: store.JobKindResearch, Prompt: "do a big thing", MaxBudgetUSD: &userCap, UserMaxBudgetUSD: &userCap, Resumable: true})
	if err != nil {
		t.Fatal(err)
	}

	// Scheduler tightens it further to fit a window's headroom — a value
	// ABOVE the user's own cap, which CapBudget must clamp down rather than
	// honor outright (this is the fix for the separate, pre-existing
	// "CapBudget can raise a cap" issue, exercised here as a precondition).
	if err := d.queue.CapBudget(ctx, created.ID, 0.10); err != nil {
		t.Fatalf("CapBudget() error: %v", err)
	}
	capped, err := d.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if capped.MaxBudgetUSD == nil || *capped.MaxBudgetUSD != userCap {
		t.Fatalf("MaxBudgetUSD after CapBudget(0.10) = %v, want clamped to the user's own cap %v", capped.MaxBudgetUSD, userCap)
	}

	t.Setenv("FAKECLAUDE_FIXTURE", "budget_capped") // fixture cost 0.05 exceeds the 0.03 user cap
	if err := d.DispatchOne(ctx, created.ID); err != nil {
		t.Fatalf("first DispatchOne() error: %v", err)
	}
	afterFirst, err := d.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterFirst.Status != store.StatusPausedBudget {
		t.Fatalf("Status after first dispatch = %q, want %q", afterFirst.Status, store.StatusPausedBudget)
	}

	// A second resume must still be refused — the user's real cap is
	// already exhausted, regardless of any scheduler pacing on top of it.
	if err := d.DispatchOne(ctx, created.ID); err != nil {
		t.Fatalf("second DispatchOne() error: %v", err)
	}
	final, err := d.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != store.StatusFailed {
		t.Errorf("Status after second dispatch = %q, want %q (user's lifetime cap exhausted, must not resume even with a scheduler cap layered on top)", final.Status, store.StatusFailed)
	}
}

func TestDispatchOne_BudgetCutoff_ErrorResult_StillPauses(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "budget_capped_error")
	d := newTestDispatcher(t)
	ctx := context.Background()

	budgetCap := 0.05
	created, err := d.queue.Enqueue(ctx, store.Job{Kind: store.JobKindResearch, Prompt: "do a big thing", MaxBudgetUSD: &budgetCap, Resumable: true})
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
	if got.Status != store.StatusPausedBudget {
		t.Errorf("Status = %q, want %q (a budget cutoff pauses for resume even when the CLI also reports is_error)", got.Status, store.StatusPausedBudget)
	}
}

func TestDispatchOne_UnderBudget_SucceedsNormally(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	d := newTestDispatcher(t)
	ctx := context.Background()

	generousCap := 5.0 // well above happy_path's 0.0230845 total_cost_usd
	created, err := d.queue.Enqueue(ctx, store.Job{Kind: store.JobKindResearch, Prompt: "say pong", MaxBudgetUSD: &generousCap})
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
		t.Errorf("Status = %q, want %q (cost stayed well under the cap, this isn't a cutoff)", got.Status, store.StatusSucceeded)
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
