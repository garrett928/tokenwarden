package api

import (
	"log"
	"net/http"
	"time"

	"tokenwarden/internal/budget"
)

// WindowUsage is the wire representation of a budget.WindowTotals. Per
// internal/budget's doc comment, these are tokenwarden's own exact spend
// in the window — not the plan's actual rate-limit fill. See
// UsageResponse.GroundTruth for the (possibly absent, possibly stale)
// authoritative reading, when one exists.
type WindowUsage struct {
	InputTokens              int     `json:"input_tokens"`
	CacheCreationInputTokens int     `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int     `json:"cache_read_input_tokens"`
	OutputTokens             int     `json:"output_tokens"`
	CostUSD                  float64 `json:"cost_usd"`
	EntryCount               int     `json:"entry_count"`
}

func newWindowUsage(t budget.WindowTotals) WindowUsage {
	return WindowUsage{
		InputTokens:              t.InputTokens,
		CacheCreationInputTokens: t.CacheCreationInputTokens,
		CacheReadInputTokens:     t.CacheReadInputTokens,
		OutputTokens:             t.OutputTokens,
		CostUSD:                  t.CostUSD,
		EntryCount:               t.EntryCount,
	}
}

// UsageResponse is the body of GET /api/usage.
type UsageResponse struct {
	FiveHour WindowUsage `json:"five_hour"`
	SevenDay WindowUsage `json:"seven_day"`
	// GroundTruth is nil until the twprobe shim has recorded at least one
	// reading — that's an expected startup state, not an error.
	GroundTruth *GroundTruthResponse `json:"ground_truth,omitempty"`
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	now := time.Now()

	fiveHour, err := s.ledger.FiveHourTotal(r.Context(), now)
	if err != nil {
		log.Printf("api: computing five-hour usage total: %v", err)
		writeError(w, http.StatusInternalServerError, "computing usage failed")
		return
	}
	sevenDay, err := s.ledger.SevenDayTotal(r.Context(), now)
	if err != nil {
		log.Printf("api: computing seven-day usage total: %v", err)
		writeError(w, http.StatusInternalServerError, "computing usage failed")
		return
	}
	groundTruth, ok, err := s.ledger.LatestGroundTruth(r.Context())
	if err != nil {
		log.Printf("api: fetching latest ground truth: %v", err)
		writeError(w, http.StatusInternalServerError, "computing usage failed")
		return
	}

	resp := UsageResponse{
		FiveHour: newWindowUsage(fiveHour),
		SevenDay: newWindowUsage(sevenDay),
	}
	if ok {
		gt := newGroundTruthResponse(groundTruth, now)
		resp.GroundTruth = &gt
	}
	writeJSON(w, http.StatusOK, resp)
}
