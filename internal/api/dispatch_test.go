package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"tokenwarden/internal/budget"
	"tokenwarden/internal/dispatch"
	"tokenwarden/internal/queue"
	"tokenwarden/internal/runner"
	"tokenwarden/internal/store"
)

// fakeClaudeBin is built once for every dispatch-endpoint test in this
// file — see internal/runner's fakeclaude harness (CLAUDE.md/NFR-TEST-1:
// never shell out to the real claude CLI in tests).
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

func newDispatchTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	q := queue.New(s)
	l := budget.New(s)
	d := dispatch.New(q, runner.New(fakeClaudeBin), l)

	srv := httptest.NewServer(NewServer(q, d, l))
	t.Cleanup(srv.Close)
	return srv
}

// pollUntilTerminal polls GET /api/jobs/{id} until the job reaches a
// terminal status or the deadline passes. The fake binary is effectively
// instant, so a generous bound here still keeps the test fast in practice.
func pollUntilTerminal(t *testing.T, srv *httptest.Server, id string) JobResponse {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(srv.URL + "/api/jobs/" + id)
		if err != nil {
			t.Fatal(err)
		}
		got := decode[JobResponse](t, resp)
		resp.Body.Close()
		if store.Status(got.Status).Terminal() {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach a terminal status within the deadline", id)
	return JobResponse{}
}

func TestDispatchJob_Success(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	srv := newDispatchTestServer(t)

	created := decode[JobResponse](t, postJSON(t, srv.URL+"/api/jobs", CreateJobRequest{
		Kind: "research", Prompt: "say pong",
	}))

	resp := postJSON(t, srv.URL+"/api/jobs/"+created.ID+"/dispatch", nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch status = %d, want 202", resp.StatusCode)
	}
	accepted := decode[JobResponse](t, resp)
	if accepted.Status != string(store.StatusRunning) {
		t.Errorf("immediate response Status = %q, want %q", accepted.Status, store.StatusRunning)
	}

	final := pollUntilTerminal(t, srv, created.ID)
	if final.Status != string(store.StatusSucceeded) {
		t.Errorf("final Status = %q, want %q", final.Status, store.StatusSucceeded)
	}
	if final.SessionID == "" {
		t.Error("SessionID is empty after a successful dispatch")
	}
	if final.Result != "pong" {
		t.Errorf("final.Result = %q, want %q", final.Result, "pong")
	}
}

func TestDispatchJob_NotFound(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	srv := newDispatchTestServer(t)

	resp := postJSON(t, srv.URL+"/api/jobs/job_nonexistent/dispatch", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestDispatchJob_AlreadyTerminal(t *testing.T) {
	t.Setenv("FAKECLAUDE_FIXTURE", "happy_path")
	srv := newDispatchTestServer(t)

	created := decode[JobResponse](t, postJSON(t, srv.URL+"/api/jobs", CreateJobRequest{
		Kind: "research", Prompt: "say pong",
	}))
	cancelResp := postJSON(t, srv.URL+"/api/jobs/"+created.ID+"/cancel", nil)
	if cancelResp.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d, want 200", cancelResp.StatusCode)
	}

	resp := postJSON(t, srv.URL+"/api/jobs/"+created.ID+"/dispatch", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}
