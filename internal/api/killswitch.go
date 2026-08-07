package api

import "net/http"

// KillSwitchResponse is the wire representation of the dispatcher's kill
// switch state (FR-SAFE-4).
type KillSwitchResponse struct {
	Halted bool `json:"halted"`
}

// handleHaltDispatch activates the kill switch: every future dispatch
// fails immediately, and every currently in-flight run is cancelled,
// terminating its claude subprocess.
func (s *Server) handleHaltDispatch(w http.ResponseWriter, r *http.Request) {
	s.dispatch.Halt()
	writeJSON(w, http.StatusOK, KillSwitchResponse{Halted: true})
}

// handleResumeDispatch deactivates the kill switch so dispatches are
// allowed again.
func (s *Server) handleResumeDispatch(w http.ResponseWriter, r *http.Request) {
	s.dispatch.Resume()
	writeJSON(w, http.StatusOK, KillSwitchResponse{Halted: false})
}

// handleKillSwitchStatus reports whether the kill switch is currently
// active.
func (s *Server) handleKillSwitchStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, KillSwitchResponse{Halted: s.dispatch.Halted()})
}
