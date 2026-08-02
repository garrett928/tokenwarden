package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default() error: %v", err)
	}
	if cfg.ListenAddr != DefaultListenAddr {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, DefaultListenAddr)
	}
	if cfg.ClaudeBinaryPath != defaultClaudeBinaryPath {
		t.Errorf("ClaudeBinaryPath = %q, want %q", cfg.ClaudeBinaryPath, defaultClaudeBinaryPath)
	}
	if cfg.DataDir == "" {
		t.Error("DataDir is empty")
	}
	if filepath.Base(cfg.DataDir) != appDirName {
		t.Errorf("DataDir = %q, want base %q", cfg.DataDir, appDirName)
	}
}

func TestResolvedDBPath(t *testing.T) {
	cfg := Config{DataDir: "/data"}
	if got, want := cfg.ResolvedDBPath(), filepath.Join("/data", dbFileName); got != want {
		t.Errorf("ResolvedDBPath() = %q, want %q", got, want)
	}

	cfg.DBPath = "/custom/path.db"
	if got, want := cfg.ResolvedDBPath(), "/custom/path.db"; got != want {
		t.Errorf("ResolvedDBPath() with explicit DBPath = %q, want %q", got, want)
	}
}

func TestLoad_MissingFileUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "does-not-exist.json"))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ListenAddr != DefaultListenAddr {
		t.Errorf("ListenAddr = %q, want default %q", cfg.ListenAddr, DefaultListenAddr)
	}
}

func TestLoad_MalformedFileErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load() with malformed JSON: want error, got nil")
	}
}

func TestLoad_FileOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body, err := json.Marshal(Config{ListenAddr: "127.0.0.1:9999"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:9999" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, "127.0.0.1:9999")
	}
	// Fields absent from the file should keep their default, not zero out.
	if cfg.ClaudeBinaryPath != defaultClaudeBinaryPath {
		t.Errorf("ClaudeBinaryPath = %q, want default %q (untouched by file)", cfg.ClaudeBinaryPath, defaultClaudeBinaryPath)
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body, err := json.Marshal(Config{ListenAddr: "127.0.0.1:9999"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TOKENWARDEN_LISTEN_ADDR", "0.0.0.0:1234")
	t.Setenv("TOKENWARDEN_CLAUDE_BINARY", "/opt/claude/bin/claude")
	t.Setenv("TOKENWARDEN_DATA_DIR", "") // explicitly unset
	t.Setenv("TOKENWARDEN_DB_PATH", "")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ListenAddr != "0.0.0.0:1234" {
		t.Errorf("ListenAddr = %q, want env override %q", cfg.ListenAddr, "0.0.0.0:1234")
	}
	if cfg.ClaudeBinaryPath != "/opt/claude/bin/claude" {
		t.Errorf("ClaudeBinaryPath = %q, want env override", cfg.ClaudeBinaryPath)
	}
}

func TestEnsureDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "tokenwarden")
	cfg := Config{DataDir: dir}
	if err := cfg.EnsureDataDir(); err != nil {
		t.Fatalf("EnsureDataDir() error: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat after EnsureDataDir: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", dir)
	}
}
