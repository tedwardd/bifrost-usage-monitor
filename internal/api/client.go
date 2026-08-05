// Package api reads a virtual key's own budget from the Bifrost gateway.
//
// The endpoint is self-service: upstream registers it outside the admin
// middleware chain, so the virtual key authorises reading its own quota and no
// admin token is needed.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	quotaPath = "/api/governance/virtual-keys/quota"
	timeout   = 30 * time.Second

	// maxBody caps the read so a misrouted request that lands on the gateway's
	// web UI cannot pull an unbounded page into memory.
	maxBody = 1 << 20
)

// AuthError means the key was missing from the request or not found in the
// config store. Retrying an unchanged key cannot succeed.
type AuthError struct {
	Status int
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("virtual key rejected (HTTP %d)", e.Status)
}

// RateLimitError carries the server's own wait, which beats guessing.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate-limited (HTTP 429), retry after %s", e.RetryAfter)
}

// Reading is one quota measurement, normalised for the cache.
type Reading struct {
	VirtualKeyName string
	IsActive       bool
	UsedDollars    float64
	// LimitDollars is the effective limit, including any active override.
	LimitDollars float64
	// Utilization is a percent in the range 0 to 100, and is 0 when
	// LimitDollars is 0, which means unlimited.
	Utilization   float64
	ResetDuration string
	LastReset     time.Time
	BudgetCount   int
}

type quotaResponse struct {
	VirtualKeyName string   `json:"virtual_key_name"`
	IsActive       bool     `json:"is_active"`
	Budgets        []budget `json:"budgets"`
}

// budget mirrors the fields we consume from upstream's TableBudget. The override
// fields are omitempty upstream and arrive as null when unset, which decodes to
// the zero value and reads correctly as "no override".
type budget struct {
	MaxLimit                float64   `json:"max_limit"`
	CurrentUsage            float64   `json:"current_usage"`
	ResetDuration           string    `json:"reset_duration"`
	LastReset               time.Time `json:"last_reset"`
	OverrideAmount          float64   `json:"override_amount"`
	OverrideMode            string    `json:"override_mode"`
	OverrideCyclesRemaining int       `json:"override_cycles_remaining"`
}

// hasActiveOverride mirrors upstream TableBudget.HasActiveOverride.
func (b budget) hasActiveOverride() bool {
	if b.OverrideAmount <= 0 {
		return false
	}
	return b.OverrideMode == "forever" ||
		(b.OverrideMode == "cycles" && b.OverrideCyclesRemaining > 0)
}

// effectiveLimit mirrors upstream TableBudget.EffectiveMaxLimit. The quota
// endpoint reports only the base max_limit, while the gateway enforces the
// larger figure, so using max_limit alone would show a red segment while
// requests are still being served.
func (b budget) effectiveLimit() float64 {
	if !b.hasActiveOverride() {
		return b.MaxLimit
	}
	return b.MaxLimit + b.OverrideAmount
}

// utilization returns percent consumed, 0 to 100.
//
// A zero limit means unlimited, not fully consumed, and must not be divided by:
// the quotient would be +Inf, which json.Marshal rejects, so every subsequent
// cache write would fail while the segment kept serving a stale reading.
func utilization(used, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	return used / limit * 100
}

var httpClient = &http.Client{Timeout: timeout}

// FetchQuota reads the budget belonging to apiKey.
func FetchQuota(baseURL, apiKey string) (Reading, error) {
	req, err := http.NewRequest(http.MethodGet, baseURL+quotaPath, nil)
	if err != nil {
		return Reading{}, err
	}
	// x-bf-vk accepts any value; the other accepted headers require an sk-bf-
	// prefix, so this one works regardless of how the key is shaped.
	req.Header.Set("x-bf-vk", apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return Reading{}, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return Reading{}, &RateLimitError{RetryAfter: retryAfter(resp)}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return Reading{}, &AuthError{Status: resp.StatusCode}
	case resp.StatusCode != http.StatusOK:
		return Reading{}, fmt.Errorf("quota fetch: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Reading{}, err
	}

	var body quotaResponse
	if err := json.Unmarshal(data, &body); err != nil {
		return Reading{}, fmt.Errorf("decode quota response: %w", err)
	}
	if len(body.Budgets) == 0 {
		return Reading{}, fmt.Errorf("no budgets on virtual key %q", body.VirtualKeyName)
	}

	b := body.Budgets[0]
	limit := b.effectiveLimit()
	return Reading{
		VirtualKeyName: body.VirtualKeyName,
		IsActive:       body.IsActive,
		UsedDollars:    b.CurrentUsage,
		LimitDollars:   limit,
		Utilization:    utilization(b.CurrentUsage, limit),
		ResetDuration:  b.ResetDuration,
		LastReset:      b.LastReset,
		BudgetCount:    len(body.Budgets),
	}, nil
}

// retryAfter reads the delay the server asked for, falling back to 60s when the
// header is absent or unparseable.
func retryAfter(resp *http.Response) time.Duration {
	const fallback = 60 * time.Second
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return fallback
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return fallback
}
