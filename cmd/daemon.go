package cmd

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"bifrost-quota-monitor/internal/api"
	"bifrost-quota-monitor/internal/cache"
	"bifrost-quota-monitor/internal/config"
)

const (
	minBackoff  = 30 * time.Second
	maxBackoff  = 300 * time.Second
	resumeGrace = 5 * time.Second

	// Shifting past this would overflow the duration and hand Ticker.Reset a
	// negative interval, which panics.
	maxFailShift = 8
)

func runDaemon() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon: load config: %v\n", err)
		os.Exit(1)
	}
	if cfg.PollIntervalSeconds <= 0 {
		cfg.PollIntervalSeconds = 300
	}

	pidPath := sharedPIDPath()
	if err := writePID(pidPath); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: write PID: %v\n", err)
		os.Exit(1)
	}
	defer os.Remove(pidPath)

	normal := time.Duration(cfg.PollIntervalSeconds) * time.Second
	cachePath := cache.Path(cfg.CachePath)

	usr1 := make(chan os.Signal, 1)
	signal.Notify(usr1, syscall.SIGUSR1)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	resume := make(chan struct{}, 1)
	go watchSleep(resume)

	// consecutiveFails is touched only on the poller's fetch goroutine, so it
	// needs no synchronisation.
	consecutiveFails := 0

	fetch := func() time.Duration {
		// Resolve both every poll rather than caching them, so a rotated key or a
		// moved gateway is picked up without a restart.
		var base string
		key, err := cfg.APIKey()
		if err == nil {
			base, err = cfg.ResolveBaseURL()
		}
		if err == nil {
			var reading api.Reading
			reading, err = api.FetchQuota(base, key)
			if err == nil {
				consecutiveFails = 0
				cache.WriteToPath(cachePath, cache.Entry{
					FetchedAt:      time.Now().UTC(),
					VirtualKeyName: reading.VirtualKeyName,
					IsActive:       reading.IsActive,
					UsedDollars:    reading.UsedDollars,
					LimitDollars:   reading.LimitDollars,
					Utilization:    reading.Utilization,
					ResetDuration:  reading.ResetDuration,
					LastReset:      reading.LastReset,
					BudgetCount:    reading.BudgetCount,
				})
				return normal
			}
		}

		// Keep the last good reading: the segment tolerates a gap up to the
		// staleness threshold, and blanking it on one bad poll wastes that.
		cache.RecordFailure(cachePath, err)

		// A 429 carries the server's own wait, which beats guessing and avoids
		// re-tripping the limit. Failures still count, so a rate-limit followed
		// by other errors keeps escalating.
		var limited *api.RateLimitError
		if errors.As(err, &limited) && limited.RetryAfter > 0 {
			consecutiveFails++
			return jittered(limited.RetryAfter)
		}

		// A rejected key is recorded and retried on the ladder rather than
		// exiting. The 300s ceiling keeps a permanently bad key cheap, and a key
		// fixed in the environment recovers without a manual restart.
		backoff := backoffFor(consecutiveFails)
		consecutiveFails++
		return jittered(backoff)
	}

	poller{
		fetch:       fetch,
		interval:    normal,
		resumeGrace: resumeGrace,
		usr1:        usr1,
		quit:        quit,
		resume:      resume,
	}.run()
}

// jittered spreads a wait over a small window above the requested duration, so
// several clients that failed together do not come back in lockstep. Only ever
// adds, so it cannot retry sooner than intended or hand Ticker.Reset a
// non-positive interval.
func jittered(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	return d + rand.N(d/4+1)
}

// backoffFor returns how long to wait given the number of failures so far. It is
// called before the counter is incremented, so the first failure waits minBackoff.
func backoffFor(consecutiveFails int) time.Duration {
	if consecutiveFails < 0 {
		return minBackoff
	}
	if consecutiveFails > maxFailShift {
		return maxBackoff
	}
	d := minBackoff << consecutiveFails
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

// poller owns the timing loop. Fetches run on a separate goroutine so a request
// in flight can never delay a signal: running fetch inline leaves SIGTERM unread
// until the HTTP timeout expires, which stalls anything waiting on the process.
type poller struct {
	// fetch performs one fetch and returns the interval to wait before the next.
	fetch       func() time.Duration
	interval    time.Duration
	resumeGrace time.Duration
	usr1        <-chan os.Signal
	quit        <-chan os.Signal
	resume      <-chan struct{}
}

func (p poller) run() {
	trigger := make(chan struct{}, 1)
	next := make(chan time.Duration, 1)

	go func() {
		for range trigger {
			select {
			case next <- p.fetch():
			default:
			}
		}
	}()

	// A full buffer means a fetch is already queued or running, so asking again
	// coalesces instead of stacking up.
	request := func() {
		select {
		case trigger <- struct{}{}:
		default:
		}
	}

	request()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	// Nil until a wake is seen; receiving on a nil channel blocks, which keeps
	// the case inert without a second select.
	var wake <-chan time.Time

	for {
		select {
		case <-ticker.C:
			request()
		case <-p.usr1:
			request()
		case <-p.resume:
			wake = time.After(p.resumeGrace)
		case <-wake:
			wake = nil
			request()
		case d := <-next:
			ticker.Reset(d)
		case <-p.quit:
			return
		}
	}
}

func writePID(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600)
}

// watchSleep detects a laptop wake by comparing wall-clock elapsed time against
// monotonic elapsed time: the monotonic clock pauses during sleep, the wall clock
// does not. macOS has no wake signal reachable without cgo.
func watchSleep(resume chan<- struct{}) {
	const checkInterval = 30 * time.Second
	const sleepThreshold = 15 * time.Second
	prev := time.Now()
	for {
		time.Sleep(checkInterval)
		now := time.Now()
		monotonicElapsed := now.Sub(prev)
		wallElapsed := now.Round(0).Sub(prev.Round(0)) // Round(0) strips monotonic
		if wallElapsed-monotonicElapsed > sleepThreshold {
			select {
			case resume <- struct{}{}:
			default:
			}
		}
		prev = now
	}
}
