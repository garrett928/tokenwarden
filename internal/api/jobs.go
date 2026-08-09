package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"tokenwarden/internal/queue"
	"tokenwarden/internal/store"
)

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var req CreateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	created, err := s.queue.Enqueue(r.Context(), req.toJob())
	if err != nil {
		writeQueueError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, newJobResponse(created))
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	var filter store.ListFilter
	for _, v := range r.URL.Query()["status"] {
		filter.Statuses = append(filter.Statuses, store.Status(v))
	}

	jobs, err := s.queue.List(r.Context(), filter)
	if err != nil {
		log.Printf("api: listing jobs: %v", err)
		writeError(w, http.StatusInternalServerError, "listing jobs failed")
		return
	}

	resp := make([]JobResponse, len(jobs))
	for i, j := range jobs {
		resp[i] = s.jobResponseWithCost(r.Context(), j)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, err := s.queue.Get(r.Context(), id)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.jobResponseWithCost(r.Context(), j))
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.queue.Cancel(r.Context(), id); err != nil {
		writeQueueError(w, err)
		return
	}

	j, err := s.queue.Get(r.Context(), id)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.jobResponseWithCost(r.Context(), j))
}

// handleDispatchJob runs a job right now. It marks the job Running
// synchronously — so a caller gets an immediate 404/409 if the job doesn't
// exist or isn't in a dispatchable state — then returns 202 and runs the
// job itself in the background, since a dispatch may take far longer than
// an HTTP request should block for. This is a manual, single-job trigger
// only: nothing here loops, retries, or picks the job on its own (see
// internal/dispatch's package comment).
func (s *Server) handleDispatchJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	job, err := s.queue.MarkRunning(r.Context(), id)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	// job.Status reflects the pre-transition state per MarkRunning's
	// contract; refetch so the response the client sees says Running.
	job.Status = store.StatusRunning

	go func() {
		// Detached from the request context: the run must continue after
		// this handler returns and the request context is cancelled.
		if err := s.dispatch.RunJob(context.Background(), job); err != nil {
			log.Printf("api: dispatching job %s: %v", id, err)
		}
	}()

	writeJSON(w, http.StatusAccepted, newJobResponse(job))
}

// jobResponseWithCost builds a JobResponse and fills in CostUSD from the
// ledger. A ledger lookup failure is logged and treated as "no cost data"
// rather than failing the whole request — cost is supplementary
// information, not something a job-detail request should 500 over.
func (s *Server) jobResponseWithCost(ctx context.Context, j store.Job) JobResponse {
	resp := newJobResponse(j)

	totals, err := s.ledger.UsageForJob(ctx, j.ID)
	if err != nil {
		log.Printf("api: getting usage for job %s: %v", j.ID, err)
		return resp
	}
	if totals.EntryCount > 0 {
		cost := totals.CostUSD
		resp.CostUSD = &cost
	}
	return resp
}

// writeQueueError maps queue/store sentinel errors to HTTP status codes.
// Anything unrecognized is logged with detail server-side and reported to
// the client as a bare 500 — internal error messages (a SQL fragment, a
// file path) don't belong in an HTTP response body.
func writeQueueError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "job not found")
	case errors.Is(err, store.ErrInvalidJob):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, queue.ErrDependencyNotFound):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, queue.ErrAlreadyTerminal):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, queue.ErrNotRunnable):
		writeError(w, http.StatusConflict, err.Error())
	default:
		log.Printf("api: unexpected error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
