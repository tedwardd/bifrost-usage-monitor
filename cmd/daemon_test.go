package cmd

import (
	"testing"
	"time"
)

// The ladder starts at the first failure, because backoffFor is called with the
// count so far rather than the incremented count.
func TestBackoffLadder(t *testing.T) {
	cases := []struct {
		fails int
		want  time.Duration
	}{
		{0, 30 * time.Second},
		{1, 60 * time.Second},
		{2, 120 * time.Second},
		{3, 240 * time.Second},
		{4, 300 * time.Second},
		{5, 300 * time.Second},
	}
	for _, c := range cases {
		if got := backoffFor(c.fails); got != c.want {
			t.Errorf("backoffFor(%d): got %v, want %v", c.fails, got, c.want)
		}
	}
}

// A large count must not overflow into a negative duration: Ticker.Reset panics
// on a non-positive interval, which would take the daemon down after a long
// outage rather than during it.
func TestBackoffStaysPositiveAtExtremes(t *testing.T) {
	for _, fails := range []int{8, 9, 64, 1000} {
		got := backoffFor(fails)
		if got <= 0 {
			t.Errorf("backoffFor(%d): got %v, must stay positive", fails, got)
		}
		if got > 300*time.Second {
			t.Errorf("backoffFor(%d): got %v, must not exceed the 300s ceiling", fails, got)
		}
	}
}

func TestBackoffNegativeInputIsFloored(t *testing.T) {
	if got := backoffFor(-1); got != 30*time.Second {
		t.Errorf("backoffFor(-1): got %v, want 30s", got)
	}
}

// Jitter only ever adds, so a retry can never fire sooner than intended and
// Ticker.Reset can never receive a non-positive interval.
func TestJitterOnlyAdds(t *testing.T) {
	base := 60 * time.Second
	for i := 0; i < 200; i++ {
		got := jittered(base)
		if got < base {
			t.Fatalf("jittered(%v) = %v, must not be less than the base", base, got)
		}
		if got > base+base/4+time.Second {
			t.Fatalf("jittered(%v) = %v, exceeds base plus a quarter", base, got)
		}
	}
}

func TestJitterLeavesNonPositiveAlone(t *testing.T) {
	if got := jittered(0); got != 0 {
		t.Errorf("jittered(0): got %v, want 0", got)
	}
}
