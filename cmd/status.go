package cmd

import (
	"fmt"

	"bifrost-quota-monitor/internal/cache"
	"bifrost-quota-monitor/internal/config"
	"bifrost-quota-monitor/internal/format"
)

func runStatus() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Print(format.StatusLine(cache.Entry{Error: err.Error()}))
		return
	}

	entry, err := cache.ReadFromPath(cache.Path(cfg.CachePath))
	if err != nil {
		// No cache file at all: the daemon has not run yet.
		fmt.Print(format.FallbackNoCache())
		return
	}

	// Print, not Println: tmux renders a trailing newline literally.
	fmt.Print(format.StatusLine(entry))
}
