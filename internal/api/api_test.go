package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tokenwarden/internal/budget"
	"tokenwarden/internal/dispatch"
	"tokenwarden/internal/queue"
	"tokenwarden/internal/runner"
	"tokenwarden/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	q := queue.New(s)
	l := budget.New(s)
	// None of these CRUD-focused tests hit the dispatch endpoint, so this
	// dispatcher is never actually invoked — internal/api/dispatch_test.go
	// covers /dispatch against a fake claude binary.
	d := dispatch.New(q, runner.New("claude"), l)

	srv := httptest.NewServer(NewServer(q, d, l))
	t.Cleanup(srv.Close)
	return srv
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decoding response body: %v", err)
	}
	return v
}

func TestHealth(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestCreateJob(t *testing.T) {
	srv := newTestServer(t)
	resp := postJSON(t, srv.URL+"/api/jobs", CreateJobRequest{
		Kind:   "research",
		Prompt: "summarize recent commits",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	got := decode[JobResponse](t, resp)
	if got.ID == "" {
		t.Error("response has no ID")
	}
	if got.Status != string(store.StatusQueued) {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusQueued)
	}
	if got.Kind != "research" {
		t.Errorf("Kind = %q, want %q", got.Kind, "research")
	}
	// Slices should serialize as [] not null for a client that doesn't
	// special-case nil.
	if got.Attachments == nil {
		t.Error("Attachments is null in JSON, want []")
	}
	if got.DependsOn == nil {
		t.Error("DependsOn is null in JSON, want []")
	}
}

func TestCreateJob_InvalidJSON(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Post(srv.URL+"/api/jobs", "application/json", bytes.NewReader([]byte("{not json")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateJob_EmptyPrompt(t *testing.T) {
	srv := newTestServer(t)
	resp := postJSON(t, srv.URL+"/api/jobs", CreateJobRequest{Kind: "research"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	body := decode[ErrorResponse](t, resp)
	if body.Error == "" {
		t.Error("error response has empty message")
	}
}

func TestCreateJob_UnknownKind(t *testing.T) {
	srv := newTestServer(t)
	resp := postJSON(t, srv.URL+"/api/jobs", CreateJobRequest{Kind: "not-a-kind", Prompt: "x"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateJob_MissingDependency(t *testing.T) {
	srv := newTestServer(t)
	resp := postJSON(t, srv.URL+"/api/jobs", CreateJobRequest{
		Kind: "research", Prompt: "x", DependsOn: []string{"job_ghost"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestGetJob(t *testing.T) {
	srv := newTestServer(t)
	created := decode[JobResponse](t, postJSON(t, srv.URL+"/api/jobs", CreateJobRequest{
		Kind: "plan", Prompt: "outline the migration",
	}))

	resp, err := http.Get(srv.URL + "/api/jobs/" + created.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := decode[JobResponse](t, resp)
	if got.ID != created.ID {
		t.Errorf("ID = %q, want %q", got.ID, created.ID)
	}
}

func TestGetJob_NotFound(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/api/jobs/job_nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestListJobs_FiltersByStatus(t *testing.T) {
	srv := newTestServer(t)
	decode[JobResponse](t, postJSON(t, srv.URL+"/api/jobs", CreateJobRequest{Kind: "research", Prompt: "a"}))
	decode[JobResponse](t, postJSON(t, srv.URL+"/api/jobs", CreateJobRequest{Kind: "research", Prompt: "b"}))

	resp, err := http.Get(srv.URL + "/api/jobs?status=queued")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := decode[[]JobResponse](t, resp)
	if len(got) != 2 {
		t.Errorf("got %d jobs, want 2", len(got))
	}

	resp2, err := http.Get(srv.URL + "/api/jobs?status=running")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	got2 := decode[[]JobResponse](t, resp2)
	if len(got2) != 0 {
		t.Errorf("got %d running jobs, want 0", len(got2))
	}
}

func TestCancelJob(t *testing.T) {
	srv := newTestServer(t)
	created := decode[JobResponse](t, postJSON(t, srv.URL+"/api/jobs", CreateJobRequest{
		Kind: "research", Prompt: "x",
	}))

	resp := postJSON(t, srv.URL+"/api/jobs/"+created.ID+"/cancel", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := decode[JobResponse](t, resp)
	if got.Status != string(store.StatusCancelled) {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusCancelled)
	}
}

func TestCancelJob_AlreadyTerminal(t *testing.T) {
	srv := newTestServer(t)
	created := decode[JobResponse](t, postJSON(t, srv.URL+"/api/jobs", CreateJobRequest{
		Kind: "research", Prompt: "x",
	}))

	first := postJSON(t, srv.URL+"/api/jobs/"+created.ID+"/cancel", nil)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first cancel status = %d, want 200", first.StatusCode)
	}

	second := postJSON(t, srv.URL+"/api/jobs/"+created.ID+"/cancel", nil)
	if second.StatusCode != http.StatusConflict {
		t.Errorf("second cancel status = %d, want 409", second.StatusCode)
	}
}

func TestCancelJob_NotFound(t *testing.T) {
	srv := newTestServer(t)
	resp := postJSON(t, srv.URL+"/api/jobs/job_nonexistent/cancel", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestCreateJob_DependencyChain(t *testing.T) {
	srv := newTestServer(t)
	dep := decode[JobResponse](t, postJSON(t, srv.URL+"/api/jobs", CreateJobRequest{
		Kind: "research", Prompt: "gather requirements",
	}))

	downstream := decode[JobResponse](t, postJSON(t, srv.URL+"/api/jobs", CreateJobRequest{
		Kind: "code", Prompt: "implement it", DependsOn: []string{dep.ID},
	}))

	if downstream.Status != string(store.StatusBlocked) {
		t.Errorf("Status = %q, want %q", downstream.Status, store.StatusBlocked)
	}
	if len(downstream.DependsOn) != 1 || downstream.DependsOn[0] != dep.ID {
		t.Errorf("DependsOn = %+v, want [%s]", downstream.DependsOn, dep.ID)
	}
}
