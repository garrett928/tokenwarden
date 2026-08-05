package runner

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// CheckVersion runs `claude --version` once and caches the result for the
// lifetime of the Runner. Per FR-EXEC-3, callers use this to surface CLI
// version drift early (e.g. at daemon startup) rather than discovering an
// incompatibility mid-dispatch.
func (r *Runner) CheckVersion(ctx context.Context) (string, error) {
	r.versionOnce.Do(func() {
		out, err := exec.CommandContext(ctx, r.claudeBinary, "--version").Output()
		if err != nil {
			r.versionErr = fmt.Errorf("checking claude version (binary %q): %w", r.claudeBinary, err)
			return
		}
		r.version = strings.TrimSpace(string(out))
	})
	return r.version, r.versionErr
}
