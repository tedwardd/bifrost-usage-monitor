package cmd

import (
	"fmt"
	"os"

	"bifrost-quota-monitor/internal/cache"
	"bifrost-quota-monitor/internal/config"
	"bifrost-quota-monitor/internal/format"
)

func runStatus() {
	plain := false
	for _, arg := range os.Args[2:] {
		switch arg {
		case "--no-color", "--no-colour", "--plain":
			plain = true
		default:
			fmt.Fprintf(os.Stderr, "status: unknown flag %s\n", arg)
			os.Exit(1)
		}
	}

	line := format.StatusLine
	noCache := format.FallbackNoCache
	if plain {
		line = format.StatusLinePlain
		noCache = format.FallbackNoCachePlain
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Print(line(cache.Entry{Error: err.Error()}))
		return
	}

	entry, err := cache.ReadFromPath(cache.Path(cfg.CachePath))
	if err != nil {
		// No cache file at all: the daemon has not run yet.
		fmt.Print(noCache())
		return
	}

	// Print, not Println: tmux renders a trailing newline literally.
	fmt.Print(line(entry))
}
