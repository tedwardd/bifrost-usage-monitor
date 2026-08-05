package api_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"bifrost-quota-monitor/internal/api"
)

// quotaBody builds a response with one budget, using the real field names.
func quotaBody(maxLimit, usage float64, extra string) string {
	return fmt.Sprintf(`{
		"virtual_key_name": "alice@example.com",
		"is_active": true,
		"budgets": [
			{
				"id": "00000000-0000-4000-8000-000000000001",
				"max_limit": %g,
				"current_usage": %g,
				"reset_duration": "1d",
				"last_reset": "2026-08-05T00:00:05.239849Z"
				%s
			}
		]
	}`, maxLimit, usage, extra)
}

func serve(t *testing.T, status int, body string, check func(*http.Request)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFetchQuotaParsesDollarsAndPath(t *testing.T) {
	var gotPath, gotVK string
	url := serve(t, 200, quotaBody(200, 159.74365, ""), func(r *http.Request) {
		gotPath = r.URL.Path
		gotVK = r.Header.Get("x-bf-vk")
	})

	got, err := api.FetchQuota(url, "sk-bf-test")
	if err != nil {
		t.Fatalf("FetchQuota: %v", err)
	}

	if gotPath != "/api/governance/virtual-keys/quota" {
		t.Errorf("path: got %q", gotPath)
	}
	if gotVK != "sk-bf-test" {
		t.Errorf("x-bf-vk: got %q", gotVK)
	}
	// Dollars, not cents. A /100 regression would show 1.5974.
	if got.UsedDollars != 159.74365 {
		t.Errorf("UsedDollars: got %v, want 159.74365", got.UsedDollars)
	}
	if got.LimitDollars != 200 {
		t.Errorf("LimitDollars: got %v, want 200", got.LimitDollars)
	}
	if got.VirtualKeyName != "alice@example.com" {
		t.Errorf("VirtualKeyName: got %q", got.VirtualKeyName)
	}
	if !got.IsActive {
		t.Error("IsActive should be true")
	}
	if got.BudgetCount != 1 {
		t.Errorf("BudgetCount: got %d, want 1", got.BudgetCount)
	}
	if got.ResetDuration != "1d" {
		t.Errorf("ResetDuration: got %q", got.ResetDuration)
	}
	if got.LastReset.IsZero() {
		t.Error("LastReset should parse")
	}
}

// THE unit test. 159.74/200 is 79.87 percent, not 0.7987. A fraction here
// truncates to 0 under int() in the renderer and paints this key green.
func TestUtilizationIsAPercentNotAFraction(t *testing.T) {
	url := serve(t, 200, quotaBody(200, 159.74365, ""), nil)

	got, err := api.FetchQuota(url, "k")
	if err != nil {
		t.Fatalf("FetchQuota: %v", err)
	}
	if got.Utilization < 79.8 || got.Utilization > 79.9 {
		t.Errorf("Utilization: got %v, want about 79.87 (percent, not fraction)", got.Utilization)
	}
}

// An active forever override raises the real ceiling, and the endpoint reports
// only the base limit, so the denominator must add it.
func TestForeverOverrideRaisesTheLimit(t *testing.T) {
	extra := `, "override_amount": 50, "override_mode": "forever"`
	url := serve(t, 200, quotaBody(200, 125, extra), nil)

	got, err := api.FetchQuota(url, "k")
	if err != nil {
		t.Fatalf("FetchQuota: %v", err)
	}
	if got.LimitDollars != 250 {
		t.Errorf("LimitDollars: got %v, want 250", got.LimitDollars)
	}
	if got.Utilization != 50 {
		t.Errorf("Utilization: got %v, want 50", got.Utilization)
	}
}

func TestCyclesOverrideCountsOnlyWhileCyclesRemain(t *testing.T) {
	live := `, "override_amount": 50, "override_mode": "cycles", "override_cycles_remaining": 2`
	url := serve(t, 200, quotaBody(200, 100, live), nil)
	got, err := api.FetchQuota(url, "k")
	if err != nil {
		t.Fatalf("FetchQuota: %v", err)
	}
	if got.LimitDollars != 250 {
		t.Errorf("with cycles remaining, LimitDollars: got %v, want 250", got.LimitDollars)
	}

	spent := `, "override_amount": 50, "override_mode": "cycles", "override_cycles_remaining": 0`
	url2 := serve(t, 200, quotaBody(200, 100, spent), nil)
	got2, err := api.FetchQuota(url2, "k")
	if err != nil {
		t.Fatalf("FetchQuota: %v", err)
	}
	if got2.LimitDollars != 200 {
		t.Errorf("with no cycles left, LimitDollars: got %v, want 200", got2.LimitDollars)
	}
}

// A zero limit means unlimited. Dividing would give +Inf, which json.Marshal
// rejects, so every later cache write would fail.
func TestZeroLimitYieldsZeroUtilizationNotInf(t *testing.T) {
	url := serve(t, 200, quotaBody(0, 15.44, ""), nil)

	got, err := api.FetchQuota(url, "k")
	if err != nil {
		t.Fatalf("FetchQuota: %v", err)
	}
	if got.LimitDollars != 0 {
		t.Errorf("LimitDollars: got %v, want 0", got.LimitDollars)
	}
	if got.Utilization != 0 {
		t.Errorf("Utilization: got %v, want 0", got.Utilization)
	}
}

// An inactive key still has a true budget figure, so it is not an error.
func TestInactiveKeyIsNotAnError(t *testing.T) {
	body := `{"virtual_key_name":"x","is_active":false,"budgets":[{"max_limit":200,"current_usage":10}]}`
	url := serve(t, 200, body, nil)

	got, err := api.FetchQuota(url, "k")
	if err != nil {
		t.Fatalf("FetchQuota should not error on an inactive key: %v", err)
	}
	if got.IsActive {
		t.Error("IsActive should be false")
	}
	if got.UsedDollars != 10 {
		t.Errorf("UsedDollars: got %v", got.UsedDollars)
	}
}

func TestEmptyBudgetsIsAnError(t *testing.T) {
	url := serve(t, 200, `{"virtual_key_name":"x","is_active":true,"budgets":[]}`, nil)

	_, err := api.FetchQuota(url, "k")
	if err == nil {
		t.Fatal("expected an error when budgets is empty")
	}
}

// Multiple budgets still render the first, and the count records the ambiguity.
func TestMultipleBudgetsUsesFirstAndRecordsCount(t *testing.T) {
	body := `{"virtual_key_name":"x","is_active":true,"budgets":[
		{"max_limit":200,"current_usage":50},
		{"max_limit":900,"current_usage":10}
	]}`
	url := serve(t, 200, body, nil)

	got, err := api.FetchQuota(url, "k")
	if err != nil {
		t.Fatalf("FetchQuota: %v", err)
	}
	if got.LimitDollars != 200 || got.UsedDollars != 50 {
		t.Errorf("should use budgets[0], got %v/%v", got.UsedDollars, got.LimitDollars)
	}
	if got.BudgetCount != 2 {
		t.Errorf("BudgetCount: got %d, want 2", got.BudgetCount)
	}
}

func TestUnauthorizedIsAuthError(t *testing.T) {
	url := serve(t, 401, `{"error":{"message":"Unauthorized"}}`, nil)

	_, err := api.FetchQuota(url, "k")
	if err == nil {
		t.Fatal("expected an error on 401")
	}
	var authErr *api.AuthError
	if !errors.As(err, &authErr) {
		t.Errorf("expected AuthError, got %T: %v", err, err)
	}
}

func TestRateLimitCarriesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(429)
	}))
	defer srv.Close()

	_, err := api.FetchQuota(srv.URL, "k")
	var limited *api.RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("expected RateLimitError, got %T: %v", err, err)
	}
	if limited.RetryAfter.Seconds() != 42 {
		t.Errorf("RetryAfter: got %v, want 42s", limited.RetryAfter)
	}
}

// HTML means the route does not exist on that host, a useful misconfiguration signal.
func TestNonJSONBodyIsAnError(t *testing.T) {
	url := serve(t, 200, `<!doctype html><html><body>Bifrost UI</body></html>`, nil)

	if _, err := api.FetchQuota(url, "k"); err == nil {
		t.Fatal("expected an error when the body is not JSON")
	}
}

func TestServerErrorIsNotAuthError(t *testing.T) {
	url := serve(t, 500, `{"error":{"message":"boom"}}`, nil)

	_, err := api.FetchQuota(url, "k")
	if err == nil {
		t.Fatal("expected an error on 500")
	}
	var authErr *api.AuthError
	if errors.As(err, &authErr) {
		t.Error("500 must not classify as AuthError, it is retryable")
	}
}
