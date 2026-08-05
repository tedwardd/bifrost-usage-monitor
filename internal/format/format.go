// Package format renders a cached reading as a tmux status segment.
package format

import (
	"fmt"

	"bifrost-quota-monitor/internal/cache"
)

const (
	label = "Bifrost: "

	// Grey, so a placeholder reads as absent data rather than a quota state.
	fallback        = "#[fg=colour244]" + label + "??#[default]"
	fallbackNoCache = "#[fg=colour244]" + label + "--#[default]"
)

// FallbackNoCache is the segment shown when no cache file exists at all, which
// means the daemon has not run yet.
func FallbackNoCache() string {
	return fallbackNoCache
}

// StatusLine renders one segment. Any hard error or a reading past the staleness
// threshold collapses to the same placeholder: the renderer draws no distinction
// between a missing key, a rejected key and an empty budget list, because none
// of them is a quota the user can act on differently.
func StatusLine(e cache.Entry) string {
	if e.Error != "" || cache.IsStale(e) {
		return fallback
	}

	body := fmt.Sprintf("%s$%.2f", label, e.UsedDollars)
	// A zero limit means unlimited. Printing "/$0.00" would read as
	// catastrophically over budget next to a green colour.
	if e.LimitDollars > 0 {
		body += fmt.Sprintf("/$%.2f", e.LimitDollars)
	}

	return colorCode(int(e.Utilization)) + body + "#[default]"
}

// colorCode picks the tmux inline style for a utilisation percent. Thresholds
// match tmux-claude-monitor so the two segments agree on what yellow means.
func colorCode(pct int) string {
	switch {
	case pct >= 90:
		return "#[fg=red]"
	case pct >= 70:
		return "#[fg=yellow]"
	default:
		return "#[fg=green]"
	}
}
