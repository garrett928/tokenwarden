package runner

// Result is what Run returns for a completed job dispatch: the structured
// ledger data a future internal/budget package will consume, plus enough
// diagnostic detail for the immediate caller (internal/dispatch) to decide
// the job's next status.
type Result struct {
	IsError    bool
	Result     string // the CLI's final text result, or an error summary when IsError
	StopReason string
	SessionID  string // enables a later --resume, even on a failed/partial run

	NumTurns      int
	DurationMS    int64
	DurationAPIMS int64
	TotalCostUSD  float64
	Usage         Usage
	ModelUsage    map[string]ModelUsage

	// RateLimitEvents accumulates every system/api_retry(error=rate_limit)
	// event observed during the run (FR-SCHED-5). The runner does not act
	// on these itself — no retry policy lives here — but a non-empty slice
	// tells a future scheduler this run was throttled by the real ceiling,
	// not just a slow response.
	RateLimitEvents []SystemEvent

	// UnparseableLines counts stdout lines that weren't valid JSON at all.
	// Forward-compat (FR-EXEC-3) tolerates unrecognized-but-valid event
	// types via UnknownEvent; this counter is the separate case of a line
	// that couldn't be decoded as JSON in the first place.
	UnparseableLines int
}

func resultFromEvent(e ResultEvent) Result {
	return Result{
		IsError:       e.IsError,
		Result:        e.Result,
		StopReason:    e.StopReason,
		SessionID:     e.SessionID,
		NumTurns:      e.NumTurns,
		DurationMS:    e.DurationMS,
		DurationAPIMS: e.DurationAPIMS,
		TotalCostUSD:  e.TotalCostUSD,
		Usage:         e.Usage,
		ModelUsage:    e.ModelUsage,
	}
}
