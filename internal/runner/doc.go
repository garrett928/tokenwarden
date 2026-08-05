// Package runner executes a single store.Job by spawning the user's
// already-authenticated `claude` binary as a subprocess (never the Agent
// SDK — see docs/REQUIREMENTS.md FR-EXEC-1) and parsing its
// `--output-format stream-json` output.
//
// This package holds no persistence or scheduling concerns of its own: it
// takes a store.Job and returns a Result. It depends only on
// tokenwarden/internal/store (for the store.Job type), never on
// internal/queue — picking which job to run next, retry policy, and
// budget-aware pacing all live above this package, in internal/dispatch and
// (later) internal/scheduler.
//
// Safety is structural, not conventional: BuildArgs's only inputs are a
// closed set of store.Job fields, there is no escape hatch for arbitrary
// extra flags anywhere in this package, and --dangerously-skip-permissions
// is never reachable (see safety.go, FR-SAFE-1).
package runner
