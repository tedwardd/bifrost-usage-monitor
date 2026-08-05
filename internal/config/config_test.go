package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bifrost-quota-monitor/internal/config"
)

func TestDefaultsWhenNoFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PollIntervalSeconds != 300 {
		t.Errorf("PollIntervalSeconds: got %d, want 300", cfg.PollIntervalSeconds)
	}
	// No default gateway: the tool cannot guess where Bifrost lives, and
	// shipping one deployment's hostname would be wrong for everyone else.
	if cfg.BaseURL != "" {
		t.Errorf("BaseURL should default to empty, got %q", cfg.BaseURL)
	}
	if cfg.APIKeyEnv != "BIFROST_API_KEY" {
		t.Errorf("APIKeyEnv: got %q", cfg.APIKeyEnv)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := config.DefaultConfig()
	want.PollIntervalSeconds = 60
	if err := config.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.PollIntervalSeconds != 60 {
		t.Errorf("PollIntervalSeconds: got %d, want 60", got.PollIntervalSeconds)
	}

	info, err := os.Stat(filepath.Join(home, ".config", "bifrost-quota-monitor", "config.json"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("config perms: got %o, want 600", info.Mode().Perm())
	}
}

// The key must never be stored in the config file, only named by it.
func TestSavedConfigNeverContainsKeyMaterial(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BIFROST_API_KEY", "sk-bf-secret-value")

	if err := config.Save(config.DefaultConfig()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "bifrost-quota-monitor", "config.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(data), "sk-bf-") {
		t.Errorf("config file contains key material: %s", data)
	}
}

func TestAPIKeyReadsNamedEnvVar(t *testing.T) {
	t.Setenv("SOME_OTHER_VAR", "sk-bf-abc")
	cfg := config.DefaultConfig()
	cfg.APIKeyEnv = "SOME_OTHER_VAR"

	got, err := cfg.APIKey()
	if err != nil {
		t.Fatalf("APIKey: %v", err)
	}
	if got != "sk-bf-abc" {
		t.Errorf("APIKey: got %q", got)
	}
}

// A missing key must name the variable, so the failure is self-diagnosing.
func TestAPIKeyErrorNamesTheVariable(t *testing.T) {
	// Isolate HOME and point the file source at a path that does not exist:
	// otherwise this reads the developer's real key file and finds a key.
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := config.DefaultConfig()
	cfg.APIKeyEnv = "DEFINITELY_UNSET_VAR_XYZ"
	cfg.APIKeyFile = filepath.Join(home, "no-such-key")

	_, err := cfg.APIKey()
	if err == nil {
		t.Fatal("expected an error for an unset variable")
	}
	if !strings.Contains(err.Error(), "DEFINITELY_UNSET_VAR_XYZ") {
		t.Errorf("error should name the variable, got: %v", err)
	}
}

func TestExpandPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := config.ExpandPath("~/x/y.json"); got != filepath.Join(home, "x", "y.json") {
		t.Errorf("ExpandPath: got %q", got)
	}
	if got := config.ExpandPath("/abs/path"); got != "/abs/path" {
		t.Errorf("ExpandPath should leave absolute paths alone, got %q", got)
	}
}

func TestAPIKeyFallsBackToFileWhenEnvUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEFINITELY_UNSET_VAR_XYZ", "")

	keyPath := filepath.Join(home, "key")
	if err := os.WriteFile(keyPath, []byte("sk-bf-from-file\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.APIKeyEnv = "DEFINITELY_UNSET_VAR_XYZ"
	cfg.APIKeyFile = keyPath

	got, err := cfg.APIKey()
	if err != nil {
		t.Fatalf("APIKey: %v", err)
	}
	// Trailing newline must be stripped, or it lands in an HTTP header.
	if got != "sk-bf-from-file" {
		t.Errorf("APIKey: got %q, want sk-bf-from-file", got)
	}
}

// The environment wins, so interactive use is unaffected by a stale key file.
func TestAPIKeyPrefersEnvOverFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BQM_TEST_KEY", "sk-bf-from-env")

	keyPath := filepath.Join(home, "key")
	if err := os.WriteFile(keyPath, []byte("sk-bf-from-file"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.APIKeyEnv = "BQM_TEST_KEY"
	cfg.APIKeyFile = keyPath

	got, err := cfg.APIKey()
	if err != nil {
		t.Fatalf("APIKey: %v", err)
	}
	if got != "sk-bf-from-env" {
		t.Errorf("APIKey: got %q, want the env value to win", got)
	}
}

// With neither source available the error must name both, since the fix differs.
func TestAPIKeyErrorNamesBothSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := config.DefaultConfig()
	cfg.APIKeyEnv = "DEFINITELY_UNSET_VAR_XYZ"
	cfg.APIKeyFile = filepath.Join(home, "absent-key")

	_, err := cfg.APIKey()
	if err == nil {
		t.Fatal("expected an error when neither source has a key")
	}
	if !strings.Contains(err.Error(), "DEFINITELY_UNSET_VAR_XYZ") {
		t.Errorf("error should name the variable, got: %v", err)
	}
	if !strings.Contains(err.Error(), "absent-key") {
		t.Errorf("error should name the file, got: %v", err)
	}
}

// A key file with loose permissions is a mistake worth refusing: it would be
// readable by any process running as another local user.
func TestAPIKeyRejectsWorldReadableKeyFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	keyPath := filepath.Join(home, "key")
	if err := os.WriteFile(keyPath, []byte("sk-bf-loose"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.APIKeyEnv = "DEFINITELY_UNSET_VAR_XYZ"
	cfg.APIKeyFile = keyPath

	if _, err := cfg.APIKey(); err == nil {
		t.Fatal("expected an error for a world-readable key file")
	}
}

func TestSaveKeyFileWritesPrivateFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := config.DefaultConfig()
	cfg.APIKeyFile = filepath.Join(home, "sub", "key")

	if err := cfg.SaveKeyFile("sk-bf-written"); err != nil {
		t.Fatalf("SaveKeyFile: %v", err)
	}
	info, err := os.Stat(cfg.APIKeyFile)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("key file perms: got %o, want 600", info.Mode().Perm())
	}

	cfg.APIKeyEnv = "DEFINITELY_UNSET_VAR_XYZ"
	got, err := cfg.APIKey()
	if err != nil {
		t.Fatalf("APIKey after SaveKeyFile: %v", err)
	}
	if got != "sk-bf-written" {
		t.Errorf("APIKey: got %q", got)
	}
}

// An unset gateway must fail with an actionable message rather than a bare
// dial error against an empty URL.
func TestResolveBaseURLErrorsWhenUnset(t *testing.T) {
	t.Setenv("BIFROST_BASE_URL", "")
	cfg := config.DefaultConfig()

	_, err := cfg.ResolveBaseURL()
	if err == nil {
		t.Fatal("expected an error when no gateway is configured")
	}
	for _, want := range []string{"base_url", "BIFROST_BASE_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestResolveBaseURLPrefersEnvThenConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.BaseURL = "https://from-config.example"

	got, err := cfg.ResolveBaseURL()
	if err != nil {
		t.Fatalf("ResolveBaseURL: %v", err)
	}
	if got != "https://from-config.example" {
		t.Errorf("got %q, want the config value", got)
	}

	t.Setenv("BIFROST_BASE_URL", "https://from-env.example")
	got, err = cfg.ResolveBaseURL()
	if err != nil {
		t.Fatalf("ResolveBaseURL: %v", err)
	}
	if got != "https://from-env.example" {
		t.Errorf("got %q, want the env value to win", got)
	}
}

// A trailing slash would produce a double slash in the request path.
func TestResolveBaseURLTrimsTrailingSlash(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.BaseURL = "https://gateway.example/"

	got, err := cfg.ResolveBaseURL()
	if err != nil {
		t.Fatalf("ResolveBaseURL: %v", err)
	}
	if got != "https://gateway.example" {
		t.Errorf("got %q, want no trailing slash", got)
	}
}
