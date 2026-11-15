package apihostingreceipt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A gateway refusal carries no deployment header. It used to surface as
// "health probe reached deployment \"\"", which hid a 429 for weeks; it must
// report the status and the problem code, never the body.
func TestVerifierReportsGatewayRefusalStatus(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("X-Faas-Request-ID", "01a1de8c03007a93b6e75b186a932b63")
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":"app_concurrency_reached","detail":"secret-looking body text"}`))
	}))
	defer srv.Close()
	v := Verifier{
		BaseURL: srv.URL, AppsDomain: "apps.example", Timeout: 300 * time.Millisecond, RetryInterval: 50 * time.Millisecond,
		Authorize: func(context.Context, string, string, time.Time) error { return nil },
	}
	got, err := v.VerifyDeployment(context.Background(), "demo", "/healthz", "dep-1")
	if VerificationRecoveryCode(err) != SmokeErrorGatewayUnavailable || got.Status != SmokeSkipped || got.StatusCode != http.StatusTooManyRequests || got.ErrorCode != SmokeErrorGatewayUnavailable {
		t.Fatalf("result = %+v err=%v, want unavailable gateway 429", got, err)
	}
	const want = "public route returned HTTP 429 (app_concurrency_reached, request 01a1de8c03007a93b6e75b186a932b63) without authenticated candidate evidence"
	if got.Error != want {
		t.Fatalf("error = %q, want %q", got.Error, want)
	}
	// imaged logs and fails the deploy with the returned error, not the
	// receipt: it must name the refusal too (prod test 2026-11-15).
	if err == nil || err.Error() != want {
		t.Fatalf("returned error = %v, want %q", err, want)
	}
	if strings.Contains(got.Error, "secret-looking") {
		t.Fatal("body content leaked into the receipt")
	}
	if calls < 2 {
		t.Fatalf("a 429 was tried %d time(s); a transient refusal must be retried within the budget", calls)
	}
}

func TestProblemCode(t *testing.T) {
	cases := []struct {
		name, contentType, body, want string
	}{
		{"problem json", "application/problem+json", `{"code":"rate_limited"}`, "rate_limited"},
		{"not json", "text/html", `{"code":"rate_limited"}`, ""},
		{"code with spaces", "application/json", `{"code":"drop table"}`, ""},
		{"no code", "application/json", `{"title":"x"}`, ""},
		{"garbage", "application/json", `<html>`, ""},
	}
	for _, tc := range cases {
		if got := problemCode(tc.contentType, []byte(tc.body)); got != tc.want {
			t.Errorf("%s: problemCode = %q, want %q", tc.name, got, tc.want)
		}
	}
}
