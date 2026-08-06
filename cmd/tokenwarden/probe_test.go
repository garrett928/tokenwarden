package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeStatusLine_EmptyExisting(t *testing.T) {
	merged, changed, err := mergeStatusLine(nil, "/usr/local/bin/twprobe")
	if err != nil {
		t.Fatalf("mergeStatusLine() error: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true when installing into an empty file")
	}

	var settings map[string]json.RawMessage
	if err := json.Unmarshal(merged, &settings); err != nil {
		t.Fatalf("merged output is not valid JSON: %v", err)
	}
	var sl statusLineConfig
	if err := json.Unmarshal(settings["statusLine"], &sl); err != nil {
		t.Fatalf("decoding statusLine: %v", err)
	}
	if sl.Type != "command" || sl.Command != "/usr/local/bin/twprobe" {
		t.Errorf("statusLine = %+v, want {command, /usr/local/bin/twprobe}", sl)
	}
}

func TestMergeStatusLine_PreservesOtherKeys(t *testing.T) {
	existing := []byte(`{"theme": "dark", "someOtherSetting": {"nested": true}}`)
	merged, changed, err := mergeStatusLine(existing, "/usr/local/bin/twprobe")
	if err != nil {
		t.Fatalf("mergeStatusLine() error: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true")
	}

	var settings map[string]json.RawMessage
	if err := json.Unmarshal(merged, &settings); err != nil {
		t.Fatalf("merged output is not valid JSON: %v", err)
	}
	if _, ok := settings["theme"]; !ok {
		t.Error("theme key was dropped during merge")
	}
	if _, ok := settings["someOtherSetting"]; !ok {
		t.Error("someOtherSetting key was dropped during merge")
	}
	if _, ok := settings["statusLine"]; !ok {
		t.Error("statusLine key was not added")
	}
}

func TestMergeStatusLine_AlreadyInstalledIsNoOp(t *testing.T) {
	existing := []byte(`{"statusLine": {"type": "command", "command": "/usr/local/bin/twprobe"}}`)
	merged, changed, err := mergeStatusLine(existing, "/usr/local/bin/twprobe")
	if err != nil {
		t.Fatalf("mergeStatusLine() error: %v", err)
	}
	if changed {
		t.Error("changed = true, want false when already installed with the same command")
	}
	if string(merged) != string(existing) {
		t.Error("merged output changed even though changed=false")
	}
}

func TestMergeStatusLine_ReplacesDifferentCommand(t *testing.T) {
	existing := []byte(`{"statusLine": {"type": "command", "command": "/some/other/script.sh"}}`)
	merged, changed, err := mergeStatusLine(existing, "/usr/local/bin/twprobe")
	if err != nil {
		t.Fatalf("mergeStatusLine() error: %v", err)
	}
	if !changed {
		t.Error("changed = false, want true when replacing a different statusLine command")
	}

	var settings map[string]json.RawMessage
	if err := json.Unmarshal(merged, &settings); err != nil {
		t.Fatal(err)
	}
	var sl statusLineConfig
	if err := json.Unmarshal(settings["statusLine"], &sl); err != nil {
		t.Fatal(err)
	}
	if sl.Command != "/usr/local/bin/twprobe" {
		t.Errorf("Command = %q, want the new probe binary path", sl.Command)
	}
}

func TestMergeStatusLine_MalformedExistingErrors(t *testing.T) {
	_, _, err := mergeStatusLine([]byte("not json"), "/usr/local/bin/twprobe")
	if err == nil {
		t.Error("mergeStatusLine() error = nil, want error for malformed existing settings")
	}
}

func TestCmdProbeInstall_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "nested", "settings.json") // parent dir doesn't exist yet
	probeBinary := filepath.Join(dir, "twprobe")
	if err := os.WriteFile(probeBinary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := cmdProbeInstall([]string{"--claude-settings", settingsPath, "--probe-binary", probeBinary}); err != nil {
		t.Fatalf("cmdProbeInstall() error: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("reading installed settings file: %v", err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("installed settings file is not valid JSON: %v", err)
	}
	var sl statusLineConfig
	if err := json.Unmarshal(settings["statusLine"], &sl); err != nil {
		t.Fatal(err)
	}
	if sl.Command != probeBinary {
		t.Errorf("installed statusLine.command = %q, want %q", sl.Command, probeBinary)
	}

	// Re-running should be a no-op — no error, no need to re-check file
	// contents since mergeStatusLine's own no-op behavior is covered above.
	if err := cmdProbeInstall([]string{"--claude-settings", settingsPath, "--probe-binary", probeBinary}); err != nil {
		t.Fatalf("second cmdProbeInstall() error: %v", err)
	}
}
