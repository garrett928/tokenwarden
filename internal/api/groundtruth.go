package api

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"tokenwarden/internal/budget"
)

// RateLimitWindow mirrors the shape of one window inside the CLI's own
// `rate_limits` statusline payload (SPIKE-001 experiment 3), so
// cmd/twprobe can forward what it reads on stdin close to verbatim rather
// than needing to reshape it.
type RateLimitWindow struct {
	UsedPercentage int   `json:"used_percentage"`
	ResetsAt       int64 `json:"resets_at"` // unix seconds
}

// RecordGroundTruthRequest is the body of POST /api/ground-truth.
type RecordGroundTruthRequest struct {
	FiveHour RateLimitWindow `json:"five_hour"`
	SevenDay RateLimitWindow `json:"seven_day"`
}

// GroundTruthResponse is the wire representation of the latest known
// ground-truth reading, embedded in UsageResponse.
type GroundTruthResponse struct {
	FiveHour   RateLimitWindow `json:"five_hour"`
	SevenDay   RateLimitWindow `json:"seven_day"`
	ObservedAt time.Time       `json:"observed_at"`
	AgeSeconds int64           `json:"age_seconds"`
}

func (s *Server) handleRecordGroundTruth(w http.ResponseWriter, r *http.Request) {
	var req RecordGroundTruthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if req.FiveHour.ResetsAt == 0 || req.SevenDay.ResetsAt == 0 {
		writeError(w, http.StatusBadRequest, "five_hour.resets_at and seven_day.resets_at are required")
		return
	}

	reading := budget.GroundTruthReading{
		FiveHourUsedPercentage: req.FiveHour.UsedPercentage,
		FiveHourResetsAt:       time.Unix(req.FiveHour.ResetsAt, 0).UTC(),
		SevenDayUsedPercentage: req.SevenDay.UsedPercentage,
		SevenDayResetsAt:       time.Unix(req.SevenDay.ResetsAt, 0).UTC(),
	}
	if err := s.ledger.RecordGroundTruth(r.Context(), reading); err != nil {
		log.Printf("api: recording ground truth: %v", err)
		writeError(w, http.StatusInternalServerError, "recording ground truth failed")
		return
	}
	writeJSON(w, http.StatusCreated, newGroundTruthResponse(reading, time.Now()))
}

func newGroundTruthResponse(r budget.GroundTruthReading, now time.Time) GroundTruthResponse {
	observedAt := r.ObservedAt
	if observedAt.IsZero() {
		observedAt = now
	}
	return GroundTruthResponse{
		FiveHour:   RateLimitWindow{UsedPercentage: r.FiveHourUsedPercentage, ResetsAt: r.FiveHourResetsAt.Unix()},
		SevenDay:   RateLimitWindow{UsedPercentage: r.SevenDayUsedPercentage, ResetsAt: r.SevenDayResetsAt.Unix()},
		ObservedAt: observedAt,
		AgeSeconds: int64(now.Sub(observedAt).Seconds()),
	}
}
