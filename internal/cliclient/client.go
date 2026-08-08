// Package cliclient is an HTTP client for the tokenwarden daemon's API,
// shared by cmd/tokenwarden. It talks JSON over HTTP using the api
// package's exported wire types directly — those types ARE the contract,
// so redefining equivalent structs here would just be a second place for
// the shape to drift out of sync.
package cliclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"tokenwarden/internal/api"
)

// Client talks to a running tokenwarden daemon.
type Client struct {
	baseURL string
	http    *http.Client
}

// New builds a Client for the daemon at baseURL, e.g. "http://127.0.0.1:7842".
func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// APIError is returned when the daemon responds with a non-2xx status. It
// carries the status code so callers can branch on it (e.g. 404 vs 409)
// without parsing the message.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("tokenwardend: %s (status %d)", e.Message, e.StatusCode)
}

// Health checks the daemon is up and responding.
func (c *Client) Health(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodGet, "/api/health", nil)
	return err
}

// CreateJob queues a new job.
func (c *Client) CreateJob(ctx context.Context, req api.CreateJobRequest) (api.JobResponse, error) {
	body, err := c.do(ctx, http.MethodPost, "/api/jobs", req)
	if err != nil {
		return api.JobResponse{}, err
	}
	return decode[api.JobResponse](body)
}

// ListJobs returns jobs, optionally filtered by status (empty statuses
// means no filter — every job).
func (c *Client) ListJobs(ctx context.Context, statuses []string) ([]api.JobResponse, error) {
	path := "/api/jobs"
	for i, s := range statuses {
		sep := "&"
		if i == 0 {
			sep = "?"
		}
		path += sep + "status=" + s
	}
	body, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	return decode[[]api.JobResponse](body)
}

// GetJob fetches a single job by ID.
func (c *Client) GetJob(ctx context.Context, id string) (api.JobResponse, error) {
	body, err := c.do(ctx, http.MethodGet, "/api/jobs/"+id, nil)
	if err != nil {
		return api.JobResponse{}, err
	}
	return decode[api.JobResponse](body)
}

// CancelJob cancels a non-terminal job.
func (c *Client) CancelJob(ctx context.Context, id string) (api.JobResponse, error) {
	body, err := c.do(ctx, http.MethodPost, "/api/jobs/"+id+"/cancel", nil)
	if err != nil {
		return api.JobResponse{}, err
	}
	return decode[api.JobResponse](body)
}

// DispatchJob runs a job right now. The daemon marks it Running and
// returns immediately (202) — the job continues running in the
// background, so a caller that wants the final outcome should poll
// GetJob until the status is terminal.
func (c *Client) DispatchJob(ctx context.Context, id string) (api.JobResponse, error) {
	body, err := c.do(ctx, http.MethodPost, "/api/jobs/"+id+"/dispatch", nil)
	if err != nil {
		return api.JobResponse{}, err
	}
	return decode[api.JobResponse](body)
}

// Usage returns tokenwarden's own rolling 5-hour/7-day usage totals — see
// api.UsageResponse for the caveat that this is exact local ledger spend,
// not the plan's actual rate-limit window fill.
func (c *Client) Usage(ctx context.Context) (api.UsageResponse, error) {
	body, err := c.do(ctx, http.MethodGet, "/api/usage", nil)
	if err != nil {
		return api.UsageResponse{}, err
	}
	return decode[api.UsageResponse](body)
}

// HaltDispatch activates the kill switch (FR-SAFE-4): every future
// dispatch fails immediately, and every currently in-flight run is
// cancelled, terminating its claude subprocess.
func (c *Client) HaltDispatch(ctx context.Context) (api.KillSwitchResponse, error) {
	body, err := c.do(ctx, http.MethodPost, "/api/kill-switch/halt", nil)
	if err != nil {
		return api.KillSwitchResponse{}, err
	}
	return decode[api.KillSwitchResponse](body)
}

// ResumeDispatch deactivates the kill switch so dispatches are allowed
// again.
func (c *Client) ResumeDispatch(ctx context.Context) (api.KillSwitchResponse, error) {
	body, err := c.do(ctx, http.MethodPost, "/api/kill-switch/resume", nil)
	if err != nil {
		return api.KillSwitchResponse{}, err
	}
	return decode[api.KillSwitchResponse](body)
}

// KillSwitchStatus reports whether the kill switch is currently active.
func (c *Client) KillSwitchStatus(ctx context.Context) (api.KillSwitchResponse, error) {
	body, err := c.do(ctx, http.MethodGet, "/api/kill-switch", nil)
	if err != nil {
		return api.KillSwitchResponse{}, err
	}
	return decode[api.KillSwitchResponse](body)
}

// GetSchedulerConfig fetches the scheduler's current config
// (REQUIREMENTS.md §5.2).
func (c *Client) GetSchedulerConfig(ctx context.Context) (api.SchedulerConfigResponse, error) {
	body, err := c.do(ctx, http.MethodGet, "/api/scheduler/config", nil)
	if err != nil {
		return api.SchedulerConfigResponse{}, err
	}
	return decode[api.SchedulerConfigResponse](body)
}

// UpdateSchedulerConfig replaces the scheduler's whole config.
func (c *Client) UpdateSchedulerConfig(ctx context.Context, req api.UpdateSchedulerConfigRequest) (api.SchedulerConfigResponse, error) {
	body, err := c.do(ctx, http.MethodPut, "/api/scheduler/config", req)
	if err != nil {
		return api.SchedulerConfigResponse{}, err
	}
	return decode[api.SchedulerConfigResponse](body)
}

func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to tokenwardend at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode >= 300 {
		msg := string(respBody)
		var errResp api.ErrorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			msg = errResp.Error
		}
		return nil, &APIError{StatusCode: resp.StatusCode, Message: msg}
	}

	return respBody, nil
}

func decode[T any](body []byte) (T, error) {
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		return v, fmt.Errorf("decoding response: %w", err)
	}
	return v, nil
}
