package budget

import (
	"context"
	"fmt"

	"tokenwarden/internal/store"
)

// CostPrediction is a per-job-kind average cost, fit from completed
// dispatches' ledger entries — REQUIREMENTS.md §6.4's "per-job cost
// predictor to decide a job is oversized in the first place." Mirrors
// CalibrationEstimate's cold-start posture (§6.3, §10 open question 3):
// below minCostSamples, Insufficient is true and Tokens/CostUSD are 0, and
// callers must fall back to dispatching without a fit rather than guessing.
type CostPrediction struct {
	Tokens       int
	CostUSD      float64
	Samples      int
	Insufficient bool
}

// minCostSamples matches minCalibrationSamples: never fit from too few
// observations (§6.3 "never a single delta").
const minCostSamples = 3

// PredictJobCost estimates what a not-yet-dispatched job of kind will cost,
// as the mean tokens and dollar cost across every Succeeded or Failed job
// of that kind seen so far.
func (l *Ledger) PredictJobCost(ctx context.Context, kind store.JobKind) (CostPrediction, error) {
	totals, err := l.store.CompletedJobUsageTotals(ctx, kind)
	if err != nil {
		return CostPrediction{}, fmt.Errorf("predicting cost for kind %s: %w", kind, err)
	}
	if len(totals) < minCostSamples {
		return CostPrediction{Samples: len(totals), Insufficient: true}, nil
	}

	var tokens int
	var cost float64
	for _, t := range totals {
		tokens += t.Tokens
		cost += t.CostUSD
	}
	return CostPrediction{
		Tokens:  tokens / len(totals),
		CostUSD: cost / float64(len(totals)),
		Samples: len(totals),
	}, nil
}
