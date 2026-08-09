package api

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"tokenwarden/internal/store"
)

// TimeBlockDTO is the wire representation of a store.TimeBlock.
// Days holds time.Weekday's int values (0=Sunday..6=Saturday); empty
// means every day.
type TimeBlockDTO struct {
	Days     []int `json:"days,omitempty"`
	StartMin int   `json:"start_min"`
	EndMin   int   `json:"end_min"`
}

func newTimeBlockDTO(b store.TimeBlock) TimeBlockDTO {
	dto := TimeBlockDTO{StartMin: b.StartMin, EndMin: b.EndMin}
	for _, d := range b.Days {
		dto.Days = append(dto.Days, int(d))
	}
	return dto
}

func (dto TimeBlockDTO) toStore() store.TimeBlock {
	b := store.TimeBlock{StartMin: dto.StartMin, EndMin: dto.EndMin}
	for _, d := range dto.Days {
		b.Days = append(b.Days, time.Weekday(d))
	}
	return b
}

// SchedulerConfigResponse is the wire representation of a
// store.SchedulerConfig (REQUIREMENTS.md §5.2).
type SchedulerConfigResponse struct {
	Enabled          bool           `json:"enabled"`
	Aggressiveness   int            `json:"aggressiveness"`
	ReservedBlocks   []TimeBlockDTO `json:"reserved_blocks"`
	PreferredWindows []TimeBlockDTO `json:"preferred_windows"`
	MaxBudgetUSD     *float64       `json:"max_budget_usd,omitempty"`
	UpdatedAt        int64          `json:"updated_at"`
}

func newSchedulerConfigResponse(cfg store.SchedulerConfig) SchedulerConfigResponse {
	resp := SchedulerConfigResponse{
		Enabled: cfg.Enabled,
		// ReservedBlocks/PreferredWindows start as []TimeBlockDTO{} rather
		// than nil so the JSON body always has "[]", never "null" — the UI
		// (and any other client) can treat these fields as always-arrays
		// without a null check.
		ReservedBlocks:   []TimeBlockDTO{},
		PreferredWindows: []TimeBlockDTO{},
		Aggressiveness:   cfg.Aggressiveness,
		MaxBudgetUSD:     cfg.MaxBudgetUSD,
		UpdatedAt:        cfg.UpdatedAt.Unix(),
	}
	for _, b := range cfg.ReservedBlocks {
		resp.ReservedBlocks = append(resp.ReservedBlocks, newTimeBlockDTO(b))
	}
	for _, b := range cfg.PreferredWindows {
		resp.PreferredWindows = append(resp.PreferredWindows, newTimeBlockDTO(b))
	}
	return resp
}

// UpdateSchedulerConfigRequest is the body of PUT /api/scheduler/config.
// It replaces the whole config — a caller that wants to change one field
// should GET first, modify, then PUT the full result back.
type UpdateSchedulerConfigRequest struct {
	Enabled          bool           `json:"enabled"`
	Aggressiveness   int            `json:"aggressiveness"`
	ReservedBlocks   []TimeBlockDTO `json:"reserved_blocks"`
	PreferredWindows []TimeBlockDTO `json:"preferred_windows"`
	MaxBudgetUSD     *float64       `json:"max_budget_usd,omitempty"`
}

func (s *Server) handleGetSchedulerConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.GetSchedulerConfig(r.Context())
	if err != nil {
		log.Printf("api: getting scheduler config: %v", err)
		writeError(w, http.StatusInternalServerError, "getting scheduler config failed")
		return
	}
	writeJSON(w, http.StatusOK, newSchedulerConfigResponse(cfg))
}

func (s *Server) handleUpdateSchedulerConfig(w http.ResponseWriter, r *http.Request) {
	var req UpdateSchedulerConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Aggressiveness < 0 || req.Aggressiveness > 100 {
		writeError(w, http.StatusBadRequest, "aggressiveness must be between 0 and 100")
		return
	}

	cfg := store.SchedulerConfig{
		Enabled:        req.Enabled,
		Aggressiveness: req.Aggressiveness,
		MaxBudgetUSD:   req.MaxBudgetUSD,
	}
	for _, b := range req.ReservedBlocks {
		cfg.ReservedBlocks = append(cfg.ReservedBlocks, b.toStore())
	}
	for _, b := range req.PreferredWindows {
		cfg.PreferredWindows = append(cfg.PreferredWindows, b.toStore())
	}

	if err := s.store.UpdateSchedulerConfig(r.Context(), cfg); err != nil {
		log.Printf("api: updating scheduler config: %v", err)
		writeError(w, http.StatusInternalServerError, "updating scheduler config failed")
		return
	}

	updated, err := s.store.GetSchedulerConfig(r.Context())
	if err != nil {
		log.Printf("api: re-fetching scheduler config: %v", err)
		writeError(w, http.StatusInternalServerError, "updating scheduler config failed")
		return
	}
	writeJSON(w, http.StatusOK, newSchedulerConfigResponse(updated))
}
