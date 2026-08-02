// Package config loads tokenwarden's configuration: defaults derived from
// the OS-appropriate config directory, layered with an optional JSON file
// and environment variable overrides. Shared by the daemon and the CLI
// client so both agree on where the daemon lives.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds daemon and client settings. Fields are exported and
// JSON-tagged so a config file can override a subset of them; anything left
// zero-valued keeps its default.
type Config struct {
	// DataDir holds the SQLite database and any per-job workspaces. Defaults
	// to the OS-appropriate user config directory plus "tokenwarden".
	DataDir string `json:"data_dir,omitempty"`

	// ListenAddr is the daemon's HTTP bind address. Loopback-only by design
	// (NFR-SEC-2) — LAN exposure is a deliberate future opt-in, not a config
	// value to widen casually.
	ListenAddr string `json:"listen_addr,omitempty"`

	// ClaudeBinaryPath is the `claude` executable to invoke. Left as the
	// bare command name by default and resolved via PATH at call time, so a
	// version mismatch surfaces as a clear runner error rather than a stale
	// absolute path silently going missing.
	ClaudeBinaryPath string `json:"claude_binary_path,omitempty"`

	// DBPath overrides where the SQLite file lives. Empty means
	// DataDir/tokenwarden.db — see ResolvedDBPath.
	DBPath string `json:"db_path,omitempty"`
}

const (
	DefaultListenAddr       = "127.0.0.1:7842"
	defaultClaudeBinaryPath = "claude"
	appDirName              = "tokenwarden"
	dbFileName              = "tokenwarden.db"
	configFileName          = "config.json"
)

// Default returns configuration with no file or environment overrides
// applied: the OS-appropriate data directory, loopback listen address, and
// "claude" resolved via PATH.
func Default() (Config, error) {
	dir, err := DefaultDataDir()
	if err != nil {
		return Config{}, err
	}
	return Config{
		DataDir:          dir,
		ListenAddr:       DefaultListenAddr,
		ClaudeBinaryPath: defaultClaudeBinaryPath,
	}, nil
}

// DefaultDataDir returns the OS-appropriate per-user config directory for
// tokenwarden: e.g. ~/Library/Application Support/tokenwarden on macOS,
// %AppData%/tokenwarden on Windows, $XDG_CONFIG_HOME/tokenwarden (or
// ~/.config/tokenwarden) on Linux.
func DefaultDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolving user config dir: %w", err)
	}
	return filepath.Join(base, appDirName), nil
}

// DefaultConfigPath returns where Load looks for a config file when none is
// given explicitly.
func DefaultConfigPath() (string, error) {
	dir, err := DefaultDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

// Load builds a Config by starting from defaults, layering a JSON file over
// it if one exists, then applying environment variable overrides.
//
// path == "" uses DefaultConfigPath(). A missing file at the resolved path
// is not an error — it just means "use defaults" — but a present, malformed
// file is, since silently ignoring bad config is how a daemon ends up
// pointed at the wrong database.
func Load(path string) (Config, error) {
	cfg, err := Default()
	if err != nil {
		return Config{}, err
	}

	if path == "" {
		path, err = DefaultConfigPath()
		if err != nil {
			return Config{}, err
		}
	}

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parsing config file %s: %w", path, err)
		}
	case os.IsNotExist(err):
		// fine — defaults stand
	default:
		return Config{}, fmt.Errorf("reading config file %s: %w", path, err)
	}

	applyEnvOverrides(&cfg)

	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("TOKENWARDEN_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("TOKENWARDEN_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("TOKENWARDEN_CLAUDE_BINARY"); v != "" {
		cfg.ClaudeBinaryPath = v
	}
	if v := os.Getenv("TOKENWARDEN_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
}

// ResolvedDBPath returns DBPath if set explicitly, otherwise DataDir joined
// with the default database filename. Computed rather than defaulted at
// load time so that overriding DataDir alone (the common case) doesn't
// require also overriding DBPath to keep them in sync.
func (c Config) ResolvedDBPath() string {
	if c.DBPath != "" {
		return c.DBPath
	}
	return filepath.Join(c.DataDir, dbFileName)
}

// EnsureDataDir creates DataDir (and any parents) if it doesn't exist.
func (c Config) EnsureDataDir() error {
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return fmt.Errorf("creating data dir %s: %w", c.DataDir, err)
	}
	return nil
}
