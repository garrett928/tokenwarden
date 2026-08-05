package runner

import "errors"

// ErrNoResultEvent is returned by Run when the claude subprocess exits
// without ever emitting a "result" stream-json event — there is nothing
// structured to build a Result from. This is distinct from a Result with
// IsError set, which means the CLI did report a result, just an
// unsuccessful one.
var ErrNoResultEvent = errors.New("claude exited without emitting a result event")

// ErrUnsupportedPermissionMode is returned by BuildArgs when a freeform
// job names a --permission-mode outside the fixed allowlist in safety.go.
// bypassPermissions is deliberately never a member of that allowlist.
var ErrUnsupportedPermissionMode = errors.New("unsupported permission mode")
