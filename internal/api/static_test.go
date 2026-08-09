package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"tokenwarden/internal/budget"
	"tokenwarden/internal/dispatch"
	"tokenwarden/internal/queue"
	"tokenwarden/internal/runner"
	"tokenwarden/internal/store"
)

func newTestServerHandler(t *testing.T) *Server {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	q := queue.New(s)
	l := budget.New(s)
	d := dispatch.New(q, runner.New("claude"), l)
	return NewServer(s, q, d, l)
}

func TestMountUI_MissingDirIsNoop(t *testing.T) {
	srv := newTestServerHandler(t)
	srv.MountUI(filepath.Join(t.TempDir(), "does-not-exist"))

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET / error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (no UI mounted)", resp.StatusCode)
	}

	// The API must still work untouched.
	resp2, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health error: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("GET /api/health status = %d, want 200", resp2.StatusCode)
	}
}

func TestMountUI_ServesStaticFilesAndSPAFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>index</html>"), 0o644); err != nil {
		t.Fatalf("writing index.html: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log('hi')"), 0o644); err != nil {
		t.Fatalf("writing app.js: %v", err)
	}

	srv := newTestServerHandler(t)
	srv.MountUI(dir)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	// Real static file.
	resp, err := http.Get(ts.URL + "/app.js")
	if err != nil {
		t.Fatalf("GET /app.js error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /app.js status = %d, want 200", resp.StatusCode)
	}

	// SPA fallback for a client-side route that isn't a real file.
	resp2, err := http.Get(ts.URL + "/jobs/some-id")
	if err != nil {
		t.Fatalf("GET /jobs/some-id error: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("GET /jobs/some-id status = %d, want 200 (SPA fallback)", resp2.StatusCode)
	}

	// The API must still take precedence over the catch-all.
	resp3, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health error: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Errorf("GET /api/health status = %d, want 200", resp3.StatusCode)
	}
}
