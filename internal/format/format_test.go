package format_test

import (
	"strings"
	"testing"
	"time"

	"bifrost-quota-monitor/internal/cache"
	"bifrost-quota-monitor/internal/format"
)

// entry builds a fresh reading at the given utilisation percent.
func entry(used, limit, pct float64) cache.Entry {
	return cache.Entry{
		FetchedAt:      time.Now(),
		VirtualKeyName: "alice@example.com",
		IsActive:       true,
		UsedDollars:    used,
		LimitDollars:   limit,
		Utilization:    pct,
		BudgetCount:    1,
	}
}

func TestRendersDollarsWithTwoDecimals(t *testing.T) {
	got := format.StatusLine(entry(15.44425975, 250, 6.18))
	if !strings.Contains(got, "Bifrost: $15.44/$250.00") {
		t.Errorf("got %q, want Bifrost: $15.44/$250.00", got)
	}
	if !strings.HasSuffix(got, "#[default]") {
		t.Errorf("style must be terminated, got %q", got)
	}
}

func TestGreenBelowSeventy(t *testing.T) {
	got := format.StatusLine(entry(100, 250, 40))
	if !strings.Contains(got, "#[fg=green]") {
		t.Errorf("expected green, got %q", got)
	}
}

// Boundary pairs. These are the tests that fail if the thresholds drift.
func TestColourBoundaries(t *testing.T) {
	cases := []struct {
		pct  float64
		want string
	}{
		{69, "#[fg=green]"},
		{70, "#[fg=yellow]"},
		{89, "#[fg=yellow]"},
		{90, "#[fg=red]"},
		{150, "#[fg=red]"},
	}
	for _, c := range cases {
		got := format.StatusLine(entry(1, 100, c.pct))
		if !strings.Contains(got, c.want) {
			t.Errorf("at %v%%: got %q, want %s", c.pct, got, c.want)
		}
	}
}

// 79.87 must be yellow. If utilisation were ever stored as the fraction 0.7987
// this would render green, which is the bug this test exists to catch.
func TestRealisticReadingIsYellow(t *testing.T) {
	got := format.StatusLine(entry(159.74, 200, 79.87))
	if !strings.Contains(got, "#[fg=yellow]") {
		t.Errorf("79.87%% should be yellow, got %q", got)
	}
}

func TestUnlimitedOmitsDenominator(t *testing.T) {
	got := format.StatusLine(entry(15.44, 0, 0))
	if !strings.Contains(got, "Bifrost: $15.44") {
		t.Errorf("got %q, want the used figure", got)
	}
	if strings.Contains(got, "/$0.00") {
		t.Errorf("a zero limit must not render as a denominator, got %q", got)
	}
	if !strings.Contains(got, "#[fg=green]") {
		t.Errorf("unlimited should be green, got %q", got)
	}
}

func TestHardErrorRendersQuestionMarks(t *testing.T) {
	e := entry(15, 250, 6)
	e.Error = "BIFROST_API_KEY is unset or empty"
	got := format.StatusLine(e)
	if !strings.Contains(got, "Bifrost: ??") {
		t.Errorf("got %q, want Bifrost: ??", got)
	}
	if !strings.Contains(got, "colour244") {
		t.Errorf("placeholder should be grey, got %q", got)
	}
}

func TestStaleReadingRendersQuestionMarks(t *testing.T) {
	e := entry(15, 250, 6)
	e.FetchedAt = time.Now().Add(-20 * time.Minute)
	if got := format.StatusLine(e); !strings.Contains(got, "Bifrost: ??") {
		t.Errorf("got %q, want Bifrost: ?? for a 20 minute old reading", got)
	}
}

// A soft error keeps the reading visible; that is the whole point of the tier.
func TestSoftErrorStillRendersTheReading(t *testing.T) {
	e := entry(15.44, 250, 6.18)
	e.LastError = "connection refused"
	e.LastErrorAt = time.Now()
	got := format.StatusLine(e)
	if !strings.Contains(got, "$15.44/$250.00") {
		t.Errorf("a soft error must not blank the reading, got %q", got)
	}
	if strings.Contains(got, "??") {
		t.Errorf("a soft error must not show ??, got %q", got)
	}
}

// An inactive key still has a true figure, so it renders normally.
func TestInactiveKeyStillRenders(t *testing.T) {
	e := entry(15.44, 250, 6.18)
	e.IsActive = false
	if got := format.StatusLine(e); !strings.Contains(got, "$15.44/$250.00") {
		t.Errorf("got %q, want the reading", got)
	}
}

func TestFallbackNoCache(t *testing.T) {
	got := format.FallbackNoCache()
	if !strings.Contains(got, "Bifrost: --") {
		t.Errorf("got %q, want Bifrost: --", got)
	}
	if !strings.Contains(got, "colour244") {
		t.Errorf("placeholder should be grey, got %q", got)
	}
}

// tmux renders a trailing newline literally, so the segment must not carry one.
func TestNoTrailingNewline(t *testing.T) {
	if strings.ContainsAny(format.StatusLine(entry(1, 100, 1)), "\n") {
		t.Error("segment must not contain a newline")
	}
	if strings.ContainsAny(format.FallbackNoCache(), "\n") {
		t.Error("placeholder must not contain a newline")
	}
}
