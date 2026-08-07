package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"tokenwarden/internal/api"
)

var twprobeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "twprobe")
	if err != nil {
		fmt.Fprintln(os.Stderr, "creating temp dir for twprobe:", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	out := filepath.Join(dir, "twprobe")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	build := exec.Command("go", "build", "-o", out, ".")
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building twprobe: %v\n%s", err, output)
		os.Exit(1)
	}
	twprobeBin = out

	os.Exit(m.Run())
}

func TestIntegration_PostsGroundTruthWhenPresent(t *testing.T) {
	var mu sync.Mutex
	var gotReq api.RecordGroundTruthRequest
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cmd := exec.Command(twprobeBin)
	cmd.Env = append(os.Environ(), "TOKENWARDEN_ADDR="+srv.URL)
	cmd.Stdin = strings.NewReader(`{
		"model": "claude-sonnet-4-5", "cwd": "/repos/app",
		"rate_limits": {
			"five_hour": {"used_percentage": 89, "resets_at": 1785642600},
			"seven_day": {"used_percentage": 22, "resets_at": 1786104000}
		}
	}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running twprobe: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "claude-sonnet-4-5 · /repos/app" {
		t.Errorf("stdout = %q, want the rendered status line", got)
	}

	// The POST is best-effort/fire-and-forget from twprobe's side, so give
	// the test server a moment to have received it before asserting.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		received := gotPath != ""
		mu.Unlock()
		if received {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/api/ground-truth" {
		t.Fatalf("daemon received path %q, want /api/ground-truth (never received a request?)", gotPath)
	}
	if gotReq.FiveHour.UsedPercentage != 89 || gotReq.SevenDay.UsedPercentage != 22 {
		t.Errorf("daemon received %+v, want 89/22", gotReq)
	}
}

func TestIntegration_NoRateLimitsSkipsPost(t *testing.T) {
	posted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posted = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cmd := exec.Command(twprobeBin)
	cmd.Env = append(os.Environ(), "TOKENWARDEN_ADDR="+srv.URL)
	cmd.Stdin = strings.NewReader(`{"model": "claude-sonnet-4-5", "cwd": "/repos/app"}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running twprobe: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "claude-sonnet-4-5 · /repos/app" {
		t.Errorf("stdout = %q, want the rendered status line", got)
	}

	// twprobe has already exited by the time Output() returns, so there's
	// no race to wait out here: if it were going to POST, it already would
	// have (or would still be blocked in-process, which Output() would
	// have waited for).
	if posted {
		t.Error("daemon received a request, want none (no rate_limits in the payload)")
	}
}

func TestIntegration_MalformedStdinExitsCleanly(t *testing.T) {
	cmd := exec.Command(twprobeBin)
	cmd.Env = append(os.Environ(), "TOKENWARDEN_ADDR=http://127.0.0.1:1")
	cmd.Stdin = strings.NewReader("not json")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("running twprobe with malformed stdin: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("stdout = %q, want empty output on malformed input", string(out))
	}
}

func TestIntegration_UnreachableDaemonDoesNotBlockOrFail(t *testing.T) {
	cmd := exec.Command(twprobeBin)
	// Port 1 is reserved and nothing will ever listen there.
	cmd.Env = append(os.Environ(), "TOKENWARDEN_ADDR=http://127.0.0.1:1")
	cmd.Stdin = strings.NewReader(`{
		"model": "claude-sonnet-4-5",
		"rate_limits": {
			"five_hour": {"used_percentage": 50, "resets_at": 1785642600},
			"seven_day": {"used_percentage": 10, "resets_at": 1786104000}
		}
	}`)

	done := make(chan error, 1)
	go func() { _, err := cmd.Output(); done <- err }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("twprobe exited with error against an unreachable daemon: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("twprobe did not exit promptly against an unreachable daemon (postTimeout should bound this)")
	}
}
