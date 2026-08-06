// Package budget is the first slice of what REQUIREMENTS.md §6 calls the
// pacing engine: the local usage ledger (§6.1 item 2 of three sensor-fusion
// sources — ground truth and calibration are future work, tracked in
// CLAUDE.md as later Phase 4 slices).
//
// Ledger records exact tokens/cost from each dispatch's runner.Result and
// answers rolling 5-hour/7-day window queries. Its totals are tokenwarden's
// own exact spend — not the plan's actual rate-limit window fill, which
// needs ground truth (a future twprobe statusline shim / PTY sentinel) to
// know. Nothing in this package should be presented to a user as "how full
// is my plan's window" without that caveat; it answers "how much has
// tokenwarden dispatched," which is a different, narrower question.
package budget
