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
