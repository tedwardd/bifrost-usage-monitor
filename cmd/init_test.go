package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// existingConfig reproduces the shape this tool has to coexist with: a plain
// status-right assignment, then another tool's managed block at the tail.
const existingConfig = `set -g status-interval 60
set -g status-left-length 30
set -g status-right '#[fg=yellow]#($HOME/.config/tmux/loadavg.sh)#[default] #[fg=white]%H:%M#[default]'

# claude-monitor begin
set -g status-right-length 200
set -ga status-right " #(  claude-monitor status)"
bind-key F5 run-shell "claude-monitor refresh"
# claude-monitor end
`

func writeConf(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tmux.conf")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

func TestPatchAppendsOurBlock(t *testing.T) {
	p := writeConf(t, existingConfig)
	if err := patchTmuxConfig(p); err != nil {
		t.Fatalf("patchTmuxConfig: %v", err)
	}
	got := readFile(t, p)

	// The command is the resolved executable path, asserted in
	// TestPatchUsesTheRunningExecutablePath; here we only check the block shape.
	for _, want := range []string{
		"# bifrost-quota-monitor begin",
		"set -g status-right-length 200",
		`set -ga status-right " #(`,
		" status)\"",
		"bind-key F6 run-shell ",
		" refresh\"",
		"# bifrost-quota-monitor end",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// The single most important test in this task: patching must not disturb the
// Claude segment or the user's own status-right.
func TestPatchPreservesClaudeBlockAndUserStatusRight(t *testing.T) {
	p := writeConf(t, existingConfig)
	if err := patchTmuxConfig(p); err != nil {
		t.Fatalf("patchTmuxConfig: %v", err)
	}
	got := readFile(t, p)

	if !strings.Contains(got, `set -ga status-right " #(  claude-monitor status)"`) {
		t.Error("the claude-monitor append was lost")
	}
	if !strings.Contains(got, "bind-key F5 run-shell") {
		t.Error("the claude-monitor F5 binding was lost")
	}
	if !strings.Contains(got, "loadavg.sh") {
		t.Error("the user's own status-right was lost")
	}
}

// We must never emit a non-append assignment, whatever the input looks like,
// because ours lands after another tool's append.
func TestPatchNeverAssignsStatusRight(t *testing.T) {
	for name, content := range map[string]string{
		"with an existing assignment": existingConfig,
		"with no assignment at all":   "set -g status-interval 60\n",
		"empty file":                  "",
	} {
		p := writeConf(t, content)
		if err := patchTmuxConfig(p); err != nil {
			t.Fatalf("%s: patchTmuxConfig: %v", name, err)
		}
		got := readFile(t, p)

		block := got[strings.Index(got, "# bifrost-quota-monitor begin"):]
		for _, line := range strings.Split(block, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "set -g status-right ") {
				t.Errorf("%s: our block assigns status-right, which would wipe a "+
					"preceding segment: %q", name, trimmed)
			}
		}
	}
}

// Re-running must replace the block, never stack a second copy.
func TestPatchIsIdempotent(t *testing.T) {
	p := writeConf(t, existingConfig)
	if err := patchTmuxConfig(p); err != nil {
		t.Fatalf("first patch: %v", err)
	}
	first := readFile(t, p)

	if err := patchTmuxConfig(p); err != nil {
		t.Fatalf("second patch: %v", err)
	}
	second := readFile(t, p)

	if first != second {
		t.Errorf("second patch changed the file:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if n := strings.Count(second, "# bifrost-quota-monitor begin"); n != 1 {
		t.Errorf("expected exactly one begin marker, got %d", n)
	}
	if n := strings.Count(second, `#(  claude-monitor status)`); n != 1 {
		t.Errorf("expected exactly one claude append, got %d", n)
	}
}

func TestPatchBacksUpTheOriginal(t *testing.T) {
	p := writeConf(t, existingConfig)
	if err := patchTmuxConfig(p); err != nil {
		t.Fatalf("patchTmuxConfig: %v", err)
	}
	backup := readFile(t, p+".bifrost-quota-monitor.bak")
	if backup != existingConfig {
		t.Errorf("backup does not match the original:\n%s", backup)
	}
}

func TestPatchCreatesMissingFileAndParents(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "tmux.conf")
	if err := patchTmuxConfig(p); err != nil {
		t.Fatalf("patchTmuxConfig: %v", err)
	}
	if !strings.Contains(readFile(t, p), "# bifrost-quota-monitor begin") {
		t.Error("expected the block in a newly created file")
	}
}

func TestTmuxAlreadyPatched(t *testing.T) {
	p := writeConf(t, existingConfig)
	if tmuxAlreadyPatched(p) {
		t.Error("should be false before patching")
	}
	if err := patchTmuxConfig(p); err != nil {
		t.Fatalf("patchTmuxConfig: %v", err)
	}
	if !tmuxAlreadyPatched(p) {
		t.Error("should be true after patching")
	}
}

// With neither candidate present, init must create the XDG path, matching the
// reference's behaviour of returning candidates[0] on no match.
func TestTmuxConfigPathPrefersExistingThenXDG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	xdg := filepath.Join(home, ".config", "tmux", "tmux.conf")
	if got := tmuxConfigPath(); got != xdg {
		t.Errorf("with neither present: got %q, want %q", got, xdg)
	}

	dotfile := filepath.Join(home, ".tmux.conf")
	if err := os.WriteFile(dotfile, []byte("set -g status-interval 60\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := tmuxConfigPath(); got != dotfile {
		t.Errorf("with only ~/.tmux.conf: got %q, want %q", got, dotfile)
	}

	if err := os.MkdirAll(filepath.Dir(xdg), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(xdg, []byte("set -g status-interval 60\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := tmuxConfigPath(); got != xdg {
		t.Errorf("with both present: got %q, want the XDG path %q", got, xdg)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", p, err)
	}
	return string(data)
}

// The block must invoke the binary that is actually running init, not a bare
// name that may not be on tmux's PATH. tmux forks a shell without the user's
// interactive PATH, and the binary may be installed under a different name.
func TestPatchUsesTheRunningExecutablePath(t *testing.T) {
	p := writeConf(t, existingConfig)
	if err := patchTmuxConfig(p); err != nil {
		t.Fatalf("patchTmuxConfig: %v", err)
	}
	got := readFile(t, p)

	prog, err := serviceProgram()
	if err != nil {
		t.Fatalf("serviceProgram: %v", err)
	}
	if !strings.Contains(got, prog+" status") {
		t.Errorf("status segment should invoke %q, got:\n%s", prog, got)
	}
	if !strings.Contains(got, prog+" refresh") {
		t.Errorf("refresh binding should invoke %q, got:\n%s", prog, got)
	}
}
