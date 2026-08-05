package cache_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bifrost-quota-monitor/internal/cache"
)

func goodEntry() cache.Entry {
	return cache.Entry{
		FetchedAt:      time.Now().UTC(),
		VirtualKeyName: "alice@example.com",
		IsActive:       true,
		UsedDollars:    160.94,
		LimitDollars:   200,
		Utilization:    80.47,
		ResetDuration:  "1d",
		BudgetCount:    1,
	}
}

func TestWriteThenReadRoundTrips(t *testing.T) {
	p := filepath.Join(t.TempDir(), "status.json")
	want := goodEntry()
	if err := cache.WriteToPath(p, want); err != nil {
		t.Fatalf("WriteToPath: %v", err)
	}

	got, err := cache.ReadFromPath(p)
	if err != nil {
		t.Fatalf("ReadFromPath: %v", err)
	}
	if got.UsedDollars != want.UsedDollars || got.LimitDollars != want.LimitDollars {
		t.Errorf("dollars: got %v/%v", got.UsedDollars, got.LimitDollars)
	}
	if got.Utilization != want.Utilization {
		t.Errorf("Utilization: got %v, want %v", got.Utilization, want.Utilization)
	}
	if got.VirtualKeyName != want.VirtualKeyName {
		t.Errorf("VirtualKeyName: got %q", got.VirtualKeyName)
	}
}

func TestWriteCreatesDirAndPrivateFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "deeper", "status.json")
	if err := cache.WriteToPath(p, goodEntry()); err != nil {
		t.Fatalf("WriteToPath: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("perms: got %o, want 600", info.Mode().Perm())
	}
}

// A write must not leave temp files behind, since the daemon writes every 300s.
func TestWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "status.json")
	for i := 0; i < 3; i++ {
		if err := cache.WriteToPath(p, goodEntry()); err != nil {
			t.Fatalf("WriteToPath: %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected only status.json, got %v", names)
	}
}

// This is the flicker guard: one bad poll must not blank a good reading.
func TestRecordFailurePreservesGoodReading(t *testing.T) {
	p := filepath.Join(t.TempDir(), "status.json")
	if err := cache.WriteToPath(p, goodEntry()); err != nil {
		t.Fatalf("WriteToPath: %v", err)
	}

	if err := cache.RecordFailure(p, errors.New("connection refused")); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}

	got, err := cache.ReadFromPath(p)
	if err != nil {
		t.Fatalf("ReadFromPath: %v", err)
	}
	if got.UsedDollars != 160.94 {
		t.Errorf("reading was discarded: UsedDollars = %v", got.UsedDollars)
	}
	if got.Error != "" {
		t.Errorf("Error should stay empty so the segment keeps rendering, got %q", got.Error)
	}
	if got.LastError != "connection refused" {
		t.Errorf("LastError: got %q", got.LastError)
	}
	if got.LastErrorAt.IsZero() {
		t.Error("LastErrorAt should be set")
	}
}

// With nothing to fall back on, the failure becomes hard so the segment shows ??.
func TestRecordFailureWithNoPriorReadingIsHard(t *testing.T) {
	p := filepath.Join(t.TempDir(), "status.json")
	if err := cache.RecordFailure(p, errors.New("boom")); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	got, err := cache.ReadFromPath(p)
	if err != nil {
		t.Fatalf("ReadFromPath: %v", err)
	}
	if got.Error != "boom" {
		t.Errorf("Error: got %q, want boom", got.Error)
	}
	if got.HasReading() {
		t.Error("HasReading should be false when Error is set")
	}
}

// A hard error must not be silently downgraded to soft on the next failure.
func TestRecordFailureOverAnErroredEntryStaysHard(t *testing.T) {
	p := filepath.Join(t.TempDir(), "status.json")
	if err := cache.WriteToPath(p, cache.Entry{FetchedAt: time.Now().UTC(), Error: "first"}); err != nil {
		t.Fatalf("WriteToPath: %v", err)
	}
	if err := cache.RecordFailure(p, errors.New("second")); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	got, _ := cache.ReadFromPath(p)
	if got.Error != "second" {
		t.Errorf("Error: got %q, want second", got.Error)
	}
}

func TestIsStaleBoundaries(t *testing.T) {
	fresh := cache.Entry{FetchedAt: time.Now().Add(-14 * time.Minute)}
	if cache.IsStale(fresh) {
		t.Error("14 minutes should not be stale")
	}
	old := cache.Entry{FetchedAt: time.Now().Add(-16 * time.Minute)}
	if !cache.IsStale(old) {
		t.Error("16 minutes should be stale")
	}
}

func TestReadMissingFileErrors(t *testing.T) {
	if _, err := cache.ReadFromPath(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("expected an error for a missing cache file")
	}
}

func TestPathExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := cache.Path("~/.cache/bifrost-quota-monitor/status.json")
	want := filepath.Join(home, ".cache", "bifrost-quota-monitor", "status.json")
	if got != want {
		t.Errorf("Path: got %q, want %q", got, want)
	}
}
