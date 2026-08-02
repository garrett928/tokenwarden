package api

import (
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
		resp[i] = newJobResponse(j)
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
	writeJSON(w, http.StatusOK, newJobResponse(j))
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
	writeJSON(w, http.StatusOK, newJobResponse(j))
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
	default:
		log.Printf("api: unexpected error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
