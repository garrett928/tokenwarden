// Package budget implements two of what REQUIREMENTS.md §6.1 calls the
// pacing engine's three sensor-fusion sources: the local usage ledger
// (item 2) and ground-truth rate_limits readings (item 1, passive capture
// path only so far — the active PTY sentinel is future work). Calibration
// (item 3, which would fuse the two into a dead-reckoned estimate) doesn't
// exist yet; see CLAUDE.md for current phase status.
//
// Ledger.RecordResult records exact tokens/cost from each dispatch's
// runner.Result and answers rolling 5-hour/7-day window queries via
// FiveHourTotal/SevenDayTotal. These totals are tokenwarden's own exact
// spend — not the plan's actual rate-limit window fill.
//
// Ledger.RecordGroundTruth/LatestGroundTruth store what the twprobe
// statusline shim (cmd/twprobe) captures from the user's own interactive
// sessions: the plan's actual rate_limits state, and the only
// authoritative source of window fill tokenwarden has.
//
// Nothing in this package should be presented to a user as "how full is my
// plan's window" by conflating the two: the ledger answers "how much has
// tokenwarden dispatched," ground truth answers "what does the plan
// actually report" (when a reading exists and however stale it is) — they
// are not yet fused into a single estimate.
package budget
