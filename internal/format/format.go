// Package format renders a cached reading as a tmux status segment.
package format

import (
	"fmt"

	"bifrost-quota-monitor/internal/cache"
)

const (
	label = "Bifrost: "

	// Bare bodies, shared by both renderings so the two cannot disagree on
	// anything except colour.
	bodyStale   = label + "??"
	bodyNoCache = label + "--"

	// Grey, so a placeholder reads as absent data rather than a quota state.
	styleMuted = "#[fg=colour244]"
	styleReset = "#[default]"
)

// FallbackNoCache is the segment shown when no cache file exists at all, which
// means the daemon has not run yet.
func FallbackNoCache() string {
	return styleMuted + bodyNoCache + styleReset
}

// FallbackNoCachePlain is FallbackNoCache without tmux style escapes.
func FallbackNoCachePlain() string {
	return bodyNoCache
}

// StatusLine renders one segment for a tmux status bar.
func StatusLine(e cache.Entry) string {
	body, ok := statusBody(e)
	if !ok {
		return styleMuted + body + styleReset
	}
	return colorCode(int(e.Utilization)) + body + styleReset
}

// StatusLinePlain renders the same segment without tmux style escapes, for a
// shell prompt, a pipe, or anywhere the escapes would show up literally.
func StatusLinePlain(e cache.Entry) string {
	body, _ := statusBody(e)
	return body
}

// statusBody builds the uncoloured segment text. ok is false for a placeholder,
// which is styled as absent data rather than as a quota level.
//
// Both renderings go through here so a change to the figures cannot land in one
// and miss the other.
func statusBody(e cache.Entry) (body string, ok bool) {
	// Any hard error or a reading past the staleness threshold collapses to the
	// same placeholder: the renderer draws no distinction between a missing key,
	// a rejected key and an empty budget list, because none of them is a quota
	// the user can act on differently.
	if e.Error != "" || cache.IsStale(e) {
		return bodyStale, false
	}

	body = fmt.Sprintf("%s$%.2f", label, e.UsedDollars)
	// A zero limit means unlimited. Printing "/$0.00" would read as
	// catastrophically over budget next to a green colour.
	if e.LimitDollars > 0 {
		body += fmt.Sprintf("/$%.2f", e.LimitDollars)
	}
	return body, true
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
