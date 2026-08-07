package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// statusLineConfig is the shape Claude Code expects for a `statusLine`
// entry in settings.json: a command it invokes on every render.
type statusLineConfig struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

func cmdProbeInstall(args []string) error {
	fs := flag.NewFlagSet("probe install", flag.ExitOnError)
	settingsPath := fs.String("claude-settings", "", "path to Claude Code's settings.json (default: ~/.claude/settings.json)")
	probeBinaryFlag := fs.String("probe-binary", "", "path to the twprobe binary (default: resolved next to this binary, or found on PATH)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	path := *settingsPath
	if path == "" {
		p, err := defaultClaudeSettingsPath()
		if err != nil {
			return err
		}
		path = p
	}

	probeBinary := *probeBinaryFlag
	if probeBinary == "" {
		p, err := resolveProbeBinary()
		if err != nil {
			return err
		}
		probeBinary = p
	}

	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	merged, changed, err := mergeStatusLine(existing, probeBinary)
	if err != nil {
		return fmt.Errorf("merging statusLine into %s: %w", path, err)
	}
	if !changed {
		fmt.Printf("twprobe already installed as the statusLine command in %s\n", path)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	// Settings likely already existed with some mode; a fresh file gets a
	// conservative default since this directory can carry other
	// machine-specific Claude Code config.
	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode()
	}
	if err := os.WriteFile(path, merged, mode); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	fmt.Printf("installed twprobe as the statusLine command in %s\n", path)
	fmt.Printf("  command: %s\n", probeBinary)
	fmt.Println("this replaces Claude Code's default status line rendering with a minimal passthrough — see docs/REQUIREMENTS.md §6.1")
	return nil
}

// mergeStatusLine merges a statusLine entry pointing at probeBinary into
// existing settings JSON (which may be empty/nil for "file doesn't exist
// yet"), preserving every other key. It reports changed=false when the
// statusLine entry already matches, so a re-run of `probe install` is a
// safe no-op. Map key ordering is not preserved on rewrite (encoding/json
// sorts map keys), which reformats but never drops existing settings.
func mergeStatusLine(existing []byte, probeBinary string) (merged []byte, changed bool, err error) {
	settings := map[string]json.RawMessage{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &settings); err != nil {
			return nil, false, fmt.Errorf("parsing existing settings JSON: %w", err)
		}
	}

	desiredJSON, err := json.Marshal(statusLineConfig{Type: "command", Command: probeBinary})
	if err != nil {
		return nil, false, err
	}

	if current, ok := settings["statusLine"]; ok && jsonEqual(current, desiredJSON) {
		return existing, false, nil
	}

	settings["statusLine"] = json.RawMessage(desiredJSON)

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}

// jsonEqual compares two JSON values by their canonical (re-marshaled)
// form rather than byte-for-byte, so whitespace/key-order differences in
// an existing statusLine entry don't cause a spurious "changed" result.
func jsonEqual(a, b json.RawMessage) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	na, errA := json.Marshal(va)
	nb, errB := json.Marshal(vb)
	return errA == nil && errB == nil && string(na) == string(nb)
}

func defaultClaudeSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// resolveProbeBinary looks for a twprobe binary next to the currently
// running tokenwarden executable first (the common case — both built into
// the same ./bin), falling back to PATH.
func resolveProbeBinary() (string, error) {
	name := "twprobe"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), name)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, nil
		}
	}

	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}

	return "", fmt.Errorf("could not find a %s binary next to tokenwarden or on PATH — build it (task build) or pass --probe-binary", name)
}
