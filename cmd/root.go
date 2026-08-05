package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func Execute() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "daemon":
		runDaemon()
	case "status":
		runStatus()
	case "refresh":
		runRefresh()
	case "init":
		runInit()
	case "--help", "-h", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// sharedPIDPath is the canonical PID file used by both daemon and refresh.
//
// Fixed rather than derived from cache_path: refresh has to find the file
// without loading the config, so both sides must agree on a constant.
func sharedPIDPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "bifrost-quota-monitor", "daemon.pid")
}

// expandHome replaces a leading ~/ with the user home directory.
func expandHome(p string) string {
	if !strings.HasPrefix(p, "~/") {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, p[2:])
}

func printUsage() {
	fmt.Print(`Usage: bifrost-quota-monitor <command>

Commands:
  init     One-time setup: verify the key, patch tmux config, install the agent
  daemon   Start the background poller (writes the cache every 5 min)
  status   Print the tmux status segment (reads the cache)
  refresh  Signal the daemon for an immediate fetch
`)
}
