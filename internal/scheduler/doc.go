// Package scheduler is the budget-aware dispatch loop (REQUIREMENTS.md §6.2)
// that internal/dispatch's doc comment calls out as a future phase's job:
// Engine.Tick decides, once per call, whether the queue's highest-priority
// job should run right now, and if so runs it via internal/dispatch.
//
// Scope of this slice: the tick loop's admission checks (reserved blocks,
// FR-SCHED-2; the aggressiveness ceiling on the 5-hour and 7-day windows,
// FR-SCHED-1/§6.2 steps 2-4) and picking+dispatching the next runnable job
// (§6.2 steps 7-8, budget-capped continuation only — declared-steps
// promotion and model-driven decomposition, §6.4 strategies 2-3, are not
// implemented here). Burn-rate-based concurrency/throttling (§6.2 steps
// 5-6) is out of scope per REQUIREMENTS.md §10 open question 2: nothing
// dispatches more than one job at a time yet, so there is no concurrency
// to widen or narrow. Cost-predicted oversized-job fitting is also not
// implemented: REQUIREMENTS.md §10 open question 3 resolved the cold-start
// case as "no predicted-cost fit, dispatch one job at a time, rely on the
// hard ceiling checks" — exactly what this package does.
package scheduler
