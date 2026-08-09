package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"tokenwarden/internal/store"
)

// fakeClaudeBin is built once by TestMain and shared by every test in this
// package — CLAUDE.md/NFR-TEST-1 require these tests never shell out to
// the real claude binary, and building fakeclaude once (rather than via
// `go run` per test) keeps the suite fast.
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
	build := exec.Command("go", "build", "-o", out, "./testdata/fakeclaude")
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building fakeclaude: %v\n%s", err, output)
		os.Exit(1)
	}
	fakeClaudeBin = out

	os.Exit(m.Run())
}

func testJob() store.Job {
	return store.Job{Kind: store.JobKindResearch, Prompt: "say pong"}
}

func TestRun_HappyPath(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	r := New(fakeClaudeBin)

	result, err := r.Run(context.Background(), testJob(), RunOptions{})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if result.IsError {
		t.Errorf("IsError = true, want false")
	}
	if result.TotalCostUSD != 0.0230845 {
		t.Errorf("TotalCostUSD = %v, want 0.0230845", result.TotalCostUSD)
	}
	if result.Usage.CacheCreation.Ephemeral1hInputTokens != 17878 {
		t.Errorf("Ephemeral1hInputTokens = %d, want 17878", result.Usage.CacheCreation.Ephemeral1hInputTokens)
	}
	mu, ok := result.ModelUsage["claude-haiku-4-5-20251001"]
	if !ok {
		t.Fatalf("ModelUsage missing expected key: %+v", result.ModelUsage)
	}
	if mu.CostUSD != 0.0230845 {
		t.Errorf("ModelUsage costUSD = %v, want 0.0230845", mu.CostUSD)
	}
	if result.SessionID != "538a1cff-0000-0000-0000-000000000001" {
		t.Errorf("SessionID = %q, want fixture session id", result.SessionID)
	}
	if len(result.RateLimitEvents) != 0 {
		t.Errorf("RateLimitEvents = %+v, want none on the happy path", result.RateLimitEvents)
	}
}

func TestRun_RateLimitSurfacedNotFatal(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "rate_limit")
	r := New(fakeClaudeBin)

	result, err := r.Run(context.Background(), testJob(), RunOptions{})
	if err != nil {
		t.Fatalf("Run() error: %v, want nil (runner has no retry policy of its own)", err)
	}
	if len(result.RateLimitEvents) != 1 {
		t.Fatalf("RateLimitEvents = %+v, want exactly 1", result.RateLimitEvents)
	}
	if result.IsError {
		t.Errorf("IsError = true, want false — the CLI's own retry succeeded")
	}
}

func TestRun_UnknownAndMalformedEventsForwardCompat(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "unknown_event")
	r := New(fakeClaudeBin)

	result, err := r.Run(context.Background(), testJob(), RunOptions{})
	if err != nil {
		t.Fatalf("Run() error: %v, want nil — neither an unknown event type nor a garbage line should be fatal", err)
	}
	if result.UnparseableLines != 1 {
		t.Errorf("UnparseableLines = %d, want 1", result.UnparseableLines)
	}
	if result.Result != "ok" {
		t.Errorf("Result = %q, want %q (the trailing valid result line must still parse)", result.Result, "ok")
	}
}

func TestRun_ErrorResultSurfacesAsStructuredData(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "error_exit")
	r := New(fakeClaudeBin)

	result, err := r.Run(context.Background(), testJob(), RunOptions{})
	if err != nil {
		t.Fatalf("Run() error: %v, want nil — a reported CLI error is data, not a Go error", err)
	}
	if !result.IsError {
		t.Errorf("IsError = false, want true")
	}
	if result.Result == "" {
		t.Errorf("Result is empty, want the CLI's error summary")
	}
}

func TestRun_NoResultEventErrors(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "no_result")
	r := New(fakeClaudeBin)

	_, err := r.Run(context.Background(), testJob(), RunOptions{})
	if !errors.Is(err, ErrNoResultEvent) {
		t.Fatalf("Run() error = %v, want ErrNoResultEvent", err)
	}
}

func TestRun_ContextCancellation(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	t.Setenv("FAKECLAUDE_DELAY_MS", "2000")
	r := New(fakeClaudeBin)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := r.Run(ctx, testJob(), RunOptions{})
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("Run() took %v to return after cancellation, want well under the fixture's 2s delay", elapsed)
	}
}

func TestCheckVersion(t *testing.T) {
	r := New(fakeClaudeBin)

	v, err := r.CheckVersion(context.Background())
	if err != nil {
		t.Fatalf("CheckVersion() error: %v", err)
	}
	if v != "2.1.152 (Claude Code)" {
		t.Errorf("CheckVersion() = %q, want %q", v, "2.1.152 (Claude Code)")
	}

	v2, err := r.CheckVersion(context.Background())
	if err != nil {
		t.Fatalf("second CheckVersion() error: %v", err)
	}
	if v2 != v {
		t.Errorf("second CheckVersion() = %q, want memoized %q", v2, v)
	}
}

// TestBuildArgs_ArgvReachesSubprocess closes the gap between BuildArgs's
// unit-level output and what the subprocess actually receives: it runs a
// real dispatch against the echoargs fixture, which reports its own argv
// back as an (deliberately unrecognized) event, and compares that against
// BuildArgs's own computation for the same job.
func TestBuildArgs_ArgvReachesSubprocess(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "echoargs")
	r := New(fakeClaudeBin)
	// A fixed SessionID makes BuildArgs deterministic (--resume <id> rather
	// than a freshly generated --session-id), so comparing two independent
	// calls to it doesn't spuriously fail on session-id randomness.
	job := store.Job{Kind: store.JobKindResearch, Prompt: "say pong", Model: "haiku", SessionID: "fixed-session-for-argv-test"}

	wantArgs, err := BuildArgs(job)
	if err != nil {
		t.Fatalf("BuildArgs() error: %v", err)
	}

	var debugEvent *UnknownEvent
	_, runErr := r.Run(context.Background(), job, RunOptions{
		OnEvent: func(e Event) {
			if ue, ok := e.(UnknownEvent); ok && ue.Type == "debug_argv" {
				debugEvent = &ue
			}
		},
	})
	// echoargs never emits a "result" event, so Run() reports that as an
	// error — the debug_argv event we care about was still forwarded via
	// OnEvent before that happened.
	if !errors.Is(runErr, ErrNoResultEvent) {
		t.Fatalf("Run() error = %v, want ErrNoResultEvent (echoargs emits no result event)", runErr)
	}
	if debugEvent == nil {
		t.Fatal("did not receive a debug_argv event from the subprocess")
	}

	var payload struct {
		Argv []string `json:"argv"`
	}
	if err := json.Unmarshal(debugEvent.Raw, &payload); err != nil {
		t.Fatalf("decoding debug_argv payload: %v", err)
	}
	if !reflect.DeepEqual(payload.Argv, wantArgs) {
		t.Errorf("subprocess argv = %v, want %v (BuildArgs output)", payload.Argv, wantArgs)
	}
}

// echoArgsCwd runs the echoargs fixture for job and returns the subprocess's
// reported working directory (see fakeclaude's echoargs handling).
func echoArgsCwd(t *testing.T, job store.Job) string {
	t.Helper()
	t.Setenv("FAKECLAUDE_FIXTURE", "echoargs")
	r := New(fakeClaudeBin)

	var debugEvent *UnknownEvent
	_, runErr := r.Run(context.Background(), job, RunOptions{
		OnEvent: func(e Event) {
			if ue, ok := e.(UnknownEvent); ok && ue.Type == "debug_argv" {
				debugEvent = &ue
			}
		},
	})
	if !errors.Is(runErr, ErrNoResultEvent) {
		t.Fatalf("Run() error = %v, want ErrNoResultEvent (echoargs emits no result event)", runErr)
	}
	if debugEvent == nil {
		t.Fatal("did not receive a debug_argv event from the subprocess")
	}

	var payload struct {
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal(debugEvent.Raw, &payload); err != nil {
		t.Fatalf("decoding debug_argv payload: %v", err)
	}
	return payload.Cwd
}

// TestRun_HonorsJobWorkspaceAsCwd guards against the daemon's own working
// directory ever leaking into a job's subprocess: a job with an explicit
// Workspace must run there, not wherever tokenwardend happens to be
// running from (which, in the real deployment, is this repo — a job
// inheriting that would pick up tokenwarden's own CLAUDE.md as context).
func TestRun_HonorsJobWorkspaceAsCwd(t *testing.T) {
	workspace := t.TempDir()
	job := store.Job{Kind: store.JobKindResearch, Prompt: "say pong", Workspace: workspace}

	gotCwd := echoArgsCwd(t, job)

	// Resolve symlinks on both sides: on macOS, t.TempDir() lives under
	// /var which is a symlink to /private/var, and the subprocess reports
	// its cwd already resolved.
	wantCwd, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("resolving workspace symlinks: %v", err)
	}
	gotResolved, err := filepath.EvalSymlinks(gotCwd)
	if err != nil {
		t.Fatalf("resolving subprocess cwd symlinks: %v", err)
	}
	if gotResolved != wantCwd {
		t.Errorf("subprocess cwd = %q, want job.Workspace %q", gotResolved, wantCwd)
	}
}

// TestRun_DefaultsToScratchDirWhenNoWorkspace guards the other half of the
// same bug: a job with no Workspace must NOT fall through to the daemon's
// own cwd — it gets a fresh scratch directory instead.
func TestRun_DefaultsToScratchDirWhenNoWorkspace(t *testing.T) {
	ownCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error: %v", err)
	}

	job := store.Job{Kind: store.JobKindResearch, Prompt: "say pong"}
	gotCwd := echoArgsCwd(t, job)

	if gotCwd == "" {
		t.Fatal("subprocess cwd is empty")
	}
	ownResolved, err := filepath.EvalSymlinks(ownCwd)
	if err != nil {
		t.Fatalf("resolving own cwd symlinks: %v", err)
	}
	gotResolved, err := filepath.EvalSymlinks(gotCwd)
	if err != nil {
		t.Fatalf("resolving subprocess cwd symlinks: %v", err)
	}
	if gotResolved == ownResolved {
		t.Errorf("subprocess cwd = %q, same as the test process's own cwd — want a fresh scratch dir", gotCwd)
	}

	// Two jobs with no Workspace must not collide on the same scratch dir.
	job2 := store.Job{Kind: store.JobKindResearch, Prompt: "say pong"}
	gotCwd2 := echoArgsCwd(t, job2)
	if gotCwd2 == gotCwd {
		t.Errorf("two unrelated jobs got the same scratch dir %q, want distinct dirs", gotCwd)
	}
}
