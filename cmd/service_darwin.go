//go:build darwin

package cmd

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	serviceDescription = "launchd user agent"
	// A distinct label is what keeps this from colliding with the Claude
	// monitor's agent in the same launchd domain.
	launchdLabel   = "com.github.tedwardd.bifrost-quota-monitor"
	launchdLogPath = "~/Library/Logs/bifrost-quota-monitor.log"

	launchctlTimeout = 15 * time.Second
)

const serviceHints = `  launchctl print gui/$(id -u)/com.github.tedwardd.bifrost-quota-monitor
  launchctl kickstart -k gui/$(id -u)/com.github.tedwardd.bifrost-quota-monitor
  tail -f ~/Library/Logs/bifrost-quota-monitor.log
`

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>daemon</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>30</integer>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`

func launchAgentPath() string {
	return expandHome("~/Library/LaunchAgents/" + launchdLabel + ".plist")
}

func renderPlist(program, logPath string) string {
	return fmt.Sprintf(plistTemplate, launchdLabel, xmlEscape(program), xmlEscape(logPath), xmlEscape(logPath))
}

// xmlEscape guards against paths containing characters that would break the
// plist, since the program path comes from the filesystem.
func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

func serviceAlreadyInstalled() bool {
	_, err := os.Stat(launchAgentPath())
	return err == nil
}

func installService() error {
	program, err := serviceProgram()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}

	logPath := expandHome(launchdLogPath)
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	plistPath := launchAgentPath()
	if err := os.MkdirAll(filepath.Dir(plistPath), 0755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	if err := os.WriteFile(plistPath, []byte(renderPlist(program, logPath)), 0644); err != nil {
		return fmt.Errorf("write agent file: %w", err)
	}
	fmt.Printf("  Agent file: %s\n", plistPath)

	domain := fmt.Sprintf("gui/%d", os.Getuid())
	target := domain + "/" + launchdLabel

	// Drop any previously loaded copy so bootstrap does not fail on the label.
	launchctl("bootout", target)
	launchctl("enable", target)

	if out, err := launchctl("bootstrap", domain, plistPath); err != nil {
		out2, err2 := launchctl("load", "-w", plistPath)
		if err2 != nil {
			return fmt.Errorf("launchctl bootstrap: %v: %s (load fallback: %v: %s)",
				err, strings.TrimSpace(string(out)), err2, strings.TrimSpace(string(out2)))
		}
	}

	// RunAtLoad already started it, so kickstart only covers a job left
	// disabled; a failure is not worth aborting an otherwise good setup.
	if out, err := launchctl("kickstart", target); err != nil {
		fmt.Fprintf(os.Stderr, "  WARNING: launchctl kickstart: %v: %s\n", err, strings.TrimSpace(string(out)))
	}

	fmt.Println("  Agent loaded and started.")
	return nil
}

// launchctl runs a subcommand under a deadline. bootout waits for the running
// daemon to terminate, so an unbounded call can stall init indefinitely.
func launchctl(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), launchctlTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "launchctl", args...).CombinedOutput()
	if ctx.Err() != nil {
		return out, fmt.Errorf("timed out after %s", launchctlTimeout)
	}
	return out, err
}
