package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StaleThreshold is how old a reading may be before the segment stops trusting
// it. Sized to absorb a short outage: a failing poll retries on a backoff ladder
// that reaches roughly 12.5 minutes after five consecutive failures.
const StaleThreshold = 15 * time.Minute

// Entry is one cached quota reading.
type Entry struct {
	FetchedAt      time.Time `json:"fetched_at"`
	VirtualKeyName string    `json:"virtual_key_name"`
	IsActive       bool      `json:"is_active"`
	UsedDollars    float64   `json:"used_dollars"`
	// LimitDollars is the effective limit: max_limit plus any active override.
	LimitDollars float64 `json:"limit_dollars"`
	// Utilization is a percent in the range 0 to 100, and is 0 when
	// LimitDollars is 0, which means unlimited rather than fully consumed.
	Utilization   float64   `json:"utilization"`
	ResetDuration string    `json:"reset_duration"`
	LastReset     time.Time `json:"last_reset"`
	// BudgetCount records how many budgets the response carried, which is what
	// tells us a real multi-budget key exists.
	BudgetCount int `json:"budget_count"`

	// Error means there is nothing to display at all.
	Error string `json:"error"`

	// LastError is a failed fetch that left an earlier reading intact. The
	// segment keeps showing that reading until FetchedAt goes stale, so a
	// transient failure does not blank the display.
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitempty"`
}

// HasReading reports whether the entry holds a usable measurement.
func (e Entry) HasReading() bool {
	return !e.FetchedAt.IsZero() && e.Error == ""
}

func IsStale(e Entry) bool {
	return time.Since(e.FetchedAt) > StaleThreshold
}

func Path(cachePath string) string {
	if strings.HasPrefix(cachePath, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, cachePath[2:])
	}
	return cachePath
}

// RecordFailure notes a failed fetch without discarding the last good reading.
// Blanking the segment on a single bad poll would waste the staleness threshold
// that exists to absorb exactly these gaps.
func RecordFailure(p string, fetchErr error) error {
	e, err := ReadFromPath(p)
	if err != nil || !e.HasReading() {
		return WriteToPath(p, Entry{FetchedAt: time.Now().UTC(), Error: fetchErr.Error()})
	}
	e.LastError = fetchErr.Error()
	e.LastErrorAt = time.Now().UTC()
	return WriteToPath(p, e)
}

// WriteToPath writes then renames, so a daemon killed mid-write leaves the
// previous cache intact rather than a truncated file for status to choke on.
func WriteToPath(p string, e Entry) error {
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".status-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func ReadFromPath(p string) (Entry, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return Entry{}, err
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return Entry{}, err
	}
	return e, nil
}
