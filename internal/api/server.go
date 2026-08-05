// Package api exposes the job queue over HTTP. It is a thin layer over
// internal/queue: request parsing, response encoding, and error-to-status
// mapping. No business logic lives here — dependency gating and priority
// ordering are queue's job, not this package's.
package api

import (
	"net/http"

	"tokenwarden/internal/budget"
	"tokenwarden/internal/dispatch"
	"tokenwarden/internal/queue"
)

// Server is an http.Handler exposing the job queue API. Construct with
// NewServer and mount however you like — the daemon wraps it directly in
// http.ListenAndServe; tests use httptest.NewServer.
type Server struct {
	queue    *queue.Queue
	dispatch *dispatch.Dispatcher
	ledger   *budget.Ledger
	mux      *http.ServeMux
}

// NewServer builds a Server backed by q, d, and l.
func NewServer(q *queue.Queue, d *dispatch.Dispatcher, l *budget.Ledger) *Server {
	s := &Server{queue: q, dispatch: d, ledger: l, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("POST /api/jobs", s.handleCreateJob)
	s.mux.HandleFunc("GET /api/jobs", s.handleListJobs)
	s.mux.HandleFunc("GET /api/jobs/{id}", s.handleGetJob)
	s.mux.HandleFunc("POST /api/jobs/{id}/cancel", s.handleCancelJob)
	s.mux.HandleFunc("POST /api/jobs/{id}/dispatch", s.handleDispatchJob)
	s.mux.HandleFunc("GET /api/usage", s.handleUsage)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
