// Package scheduler is the budget-aware dispatch loop (REQUIREMENTS.md §6.2)
// that internal/dispatch's doc comment calls out as a future phase's job:
// Engine.Tick decides, once per call, whether the queue's highest-priority
// runnable job should run right now, and if so runs it via internal/dispatch.
//
// Scope of this slice: the tick loop's admission checks (reserved blocks,
// FR-SCHED-2; the aggressiveness ceiling on the 5-hour and 7-day windows,
// FR-SCHED-1/§6.2 steps 2-4), and §6.2 step 7/§6.4's oversized-job fitting —
// FitJob predicts a candidate's cost from budget.Ledger.PredictJobCost
// (a per-kind average, cold-start gated the same way calibration is) and,
// when it doesn't fit remaining five-hour headroom, either caps the job's
// budget and dispatches it anyway if it's Resumable (strategy 1) or defers
// it and tries the next-best candidate (strategy 4). Declared-steps
// promotion and model-driven decomposition (§6.4 strategies 2-3) are not
// implemented — a non-Resumable oversized job always defers rather than
// attempting either. Burn-rate-based concurrency/throttling (§6.2 steps 5-6)
// is out of scope per REQUIREMENTS.md §10 open question 2: nothing
// dispatches more than one job at a time yet, so there is no concurrency to
// widen or narrow.
package scheduler
