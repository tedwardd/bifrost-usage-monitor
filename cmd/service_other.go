//go:build !darwin

package cmd

import (
	"fmt"
	"runtime"
)

// This file supplies the service hooks for platforms with no installer yet, so
// the rest of the tool still builds and runs there. Everything except the
// background service is platform independent: the daemon, status, refresh, and
// tmux patching all work, they just need the daemon supervised by hand.

const serviceDescription = "background service (unsupported on this platform)"

const serviceHints = `  This platform has no service installer yet. Run the daemon under your own
  supervisor, for example a systemd user unit that runs:

    bifrost-quota-monitor daemon
`

// serviceAlreadyInstalled reports false so init always reaches installService
// and prints its explanation rather than silently claiming to have skipped a
// step it cannot perform.
func serviceAlreadyInstalled() bool { return false }

func installService() error {
	return fmt.Errorf("no service installer for %s: run 'bifrost-quota-monitor daemon' under your own supervisor", runtime.GOOS)
}
