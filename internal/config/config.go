package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds the daemon's settings. The API key itself is never stored here,
// only where to find it: an environment variable, or a private file for the
// launchd agent, which does not inherit a login shell's environment.
type Config struct {
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	CachePath           string `json:"cache_path"`
	BaseURL             string `json:"base_url"`
	APIKeyEnv           string `json:"api_key_env"`
	APIKeyFile          string `json:"api_key_file"`
}

// BaseURLEnv overrides the configured gateway, which keeps a throwaway or
// second deployment reachable without editing the config file.
const BaseURLEnv = "BIFROST_BASE_URL"

func DefaultConfig() Config {
	return Config{
		PollIntervalSeconds: 300,
		CachePath:           "~/.cache/bifrost-quota-monitor/status.json",
		// No default gateway. Bifrost is self-hosted, so there is no address
		// worth guessing, and baking in one deployment's hostname would be
		// wrong for everybody else.
		BaseURL:    "",
		APIKeyEnv:  "BIFROST_API_KEY",
		APIKeyFile: "~/.config/bifrost-quota-monitor/key",
	}
}

// ResolveBaseURL returns the gateway address, preferring the environment.
//
// An unset gateway is a configuration error rather than something to guess at,
// and the message names both places it can be set: an empty base URL would
// otherwise surface as an opaque failure to dial a relative path.
func (c Config) ResolveBaseURL() (string, error) {
	raw := os.Getenv(BaseURLEnv)
	if raw == "" {
		raw = c.BaseURL
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("no Bifrost gateway configured: set base_url in %s or export %s",
			Path(), BaseURLEnv)
	}
	// A trailing slash would join into a double slash ahead of the API path.
	return strings.TrimRight(raw, "/"), nil
}

// APIKey reads the virtual key, preferring the environment so an interactive
// run always uses the current shell's value.
//
// The file exists for the launchd agent: launchd starts jobs with only the
// user-level environment, never a login shell's, so an exported variable is
// invisible to it and the daemon would report the key as unset forever.
//
// The error names both sources, because the fix differs depending on which one
// the caller meant to use.
func (c Config) APIKey() (string, error) {
	envName := c.APIKeyEnv
	if envName == "" {
		envName = DefaultConfig().APIKeyEnv
	}
	if v := os.Getenv(envName); v != "" {
		return v, nil
	}

	path := ExpandPath(c.APIKeyFile)
	if path == "" {
		return "", fmt.Errorf("%s is unset or empty and no api_key_file is configured", envName)
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s is unset or empty and %s is unreadable: %w", envName, path, err)
	}
	// A key readable by other local users is a mistake worth refusing rather
	// than quietly honouring.
	if perm := info.Mode().Perm(); perm&0077 != 0 {
		return "", fmt.Errorf("%s has permissions %o, want 600", path, perm)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", fmt.Errorf("%s is unset or empty and %s is empty", envName, path)
	}
	return key, nil
}

// SaveKeyFile stores the key for the launchd agent to read.
func (c Config) SaveKeyFile(key string) error {
	path := ExpandPath(c.APIKeyFile)
	if path == "" {
		return fmt.Errorf("no api_key_file configured")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(key+"\n"), 0600)
}

func Path() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "bifrost-quota-monitor", "config.json")
}

// Load returns defaults when no config file exists, so the binary works before
// init has run.
func Load() (Config, error) {
	data, err := os.ReadFile(Path())
	if os.IsNotExist(err) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, err
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Save(cfg Config) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}

// ExpandPath replaces a leading ~/ with the user home directory.
func ExpandPath(p string) string {
	if !strings.HasPrefix(p, "~/") {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, p[2:])
}
