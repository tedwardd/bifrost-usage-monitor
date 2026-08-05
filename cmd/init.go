package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"bifrost-quota-monitor/internal/api"
	"bifrost-quota-monitor/internal/cache"
	"bifrost-quota-monitor/internal/config"
	"bifrost-quota-monitor/internal/format"
)

const (
	markerBegin = "# bifrost-quota-monitor begin"
	markerEnd   = "# bifrost-quota-monitor end"
)

// managedBlockRe matches a block written by an earlier run, including any blank
// lines leading up to it, so removal does not leave a growing gap behind.
var managedBlockRe = regexp.MustCompile(`(?s)\n*# bifrost-quota-monitor begin\n.*?# bifrost-quota-monitor end\n?`)

func runInit() {
	force := false
	for _, arg := range os.Args[2:] {
		if arg == "--force" {
			force = true
		}
	}

	fmt.Println("=== bifrost-quota-monitor init ===")

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: load config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\n[1/4] Checking the virtual key...")
	key, err := cfg.APIKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		fmt.Fprintf(os.Stderr, "Export the key, or set api_key_env in %s.\n", config.Path())
		os.Exit(1)
	}
	fmt.Printf("  Key found in %s.\n", cfg.APIKeyEnv)

	base, err := cfg.ResolveBaseURL()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	// Persist the resolved gateway so the launchd agent, which sees no shell
	// environment, has it in the config rather than only in this shell.
	cfg.BaseURL = base
	fmt.Printf("  Gateway: %s\n", base)

	fmt.Println("\n[2/4] Fetching the quota...")
	reading, err := api.FetchQuota(base, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: quota fetch failed: %v\n", err)
		os.Exit(1)
	}
	entry := cache.Entry{
		FetchedAt:      time.Now().UTC(),
		VirtualKeyName: reading.VirtualKeyName,
		IsActive:       reading.IsActive,
		UsedDollars:    reading.UsedDollars,
		LimitDollars:   reading.LimitDollars,
		Utilization:    reading.Utilization,
		ResetDuration:  reading.ResetDuration,
		LastReset:      reading.LastReset,
		BudgetCount:    reading.BudgetCount,
	}
	if err := cache.WriteToPath(cache.Path(cfg.CachePath), entry); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: write cache: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Key %s: %s\n", reading.VirtualKeyName, format.StatusLine(entry))

	fmt.Println("\n[3/4] Writing config and patching tmux...")
	if !force && configExists() {
		fmt.Printf("  Config already at %s, skipping.\n", config.Path())
	} else {
		if err := config.Save(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: save config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("  Config written to %s\n", config.Path())
	}

	// The launchd agent starts without a login shell's environment, so it cannot
	// see an exported key. Persist it privately or the background daemon would
	// report the key as unset on every poll.
	if err := cfg.SaveKeyFile(key); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: save key file: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Key stored for the agent at %s (mode 600)\n", config.ExpandPath(cfg.APIKeyFile))

	tmuxConf := tmuxConfigPath()
	if !force && tmuxAlreadyPatched(tmuxConf) {
		fmt.Println("  tmux config already patched, skipping.")
	} else if err := patchTmuxConfig(tmuxConf); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: patch tmux: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n[4/4] Installing the %s...\n", serviceDescription)
	if !force && serviceAlreadyInstalled() {
		fmt.Println("  Service already installed, skipping.")
	} else if err := installService(); err != nil {
		// Not fatal. The three steps above already did the useful work, and on a
		// platform with no installer the only thing missing is supervision, which
		// serviceHints explains how to arrange.
		fmt.Fprintf(os.Stderr, "  WARNING: %v\n", err)
	}

	if out, err := exec.Command("tmux", "source", tmuxConf).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: tmux reload failed: %v: %s\n", err, out)
	} else {
		fmt.Println("  tmux config reloaded.")
	}

	fmt.Printf(`
=== Setup complete ===

To manage the service:
%s
Keybinding: <prefix> F6  ->  manual refresh
`, serviceHints)
}

func configExists() bool {
	_, err := os.Stat(config.Path())
	return err == nil
}

func tmuxAlreadyPatched(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), markerBegin)
}

// tmuxConfigPath returns the config to patch, preferring one that already exists
// over the XDG default, which is created when neither is present.
func tmuxConfigPath() string {
	candidates := []string{"~/.config/tmux/tmux.conf", "~/.tmux.conf"}
	for _, c := range candidates {
		if _, err := os.Stat(expandHome(c)); err == nil {
			return expandHome(c)
		}
	}
	return expandHome(candidates[0])
}

// backupTmuxConfig keeps a copy before rewriting, since this edits a file the
// user maintains by hand.
func backupTmuxConfig(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.WriteFile(path+".bifrost-quota-monitor.bak", data, 0644)
}

// patchTmuxConfig appends our managed block, replacing any earlier copy.
//
// The block deliberately never assigns status-right. The reference tool emits a
// default assignment when the config has none, which is safe for a first
// installer but not for ours: this block lands after another tool's `-ga`
// append, so a plain `set -g` here would discard that segment. Appending only
// means we consume nothing of anyone else's.
//
// Stacking is prevented by whatever plain assignment exists earlier in the
// user's file, which resets the option each time tmux sources it.
func patchTmuxConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(data)

	if err := backupTmuxConfig(path); err != nil {
		return fmt.Errorf("back up config: %w", err)
	}

	// Drop the block from any earlier run so re-patching replaces it.
	content = managedBlockRe.ReplaceAllString(content, "")
	content = strings.TrimRight(content, "\n")
	if content != "" {
		content += "\n"
	}

	// Invoke the binary by resolved path rather than by name. tmux forks a shell
	// without the user's interactive PATH, and the binary may be installed under
	// a different name than the project's, so a bare name can silently produce an
	// empty segment.
	prog, err := serviceProgram()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}

	block := "\n" + strings.Join([]string{
		markerBegin,
		"set -g status-right-length 200",
		`set -ga status-right " #(` + prog + ` status)"`,
		`bind-key F6 run-shell "` + prog + ` refresh"`,
		markerEnd,
	}, "\n") + "\n"

	if err := os.WriteFile(path, []byte(content+block), 0644); err != nil {
		return err
	}
	fmt.Printf("  Patched: %s\n", path)
	return nil
}
