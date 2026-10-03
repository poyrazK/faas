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
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != SmokeFailed || got.StatusCode != http.StatusTooManyRequests || got.ErrorCode != "smoke_http_status" {
		t.Fatalf("result = %+v, want a failed smoke_http_status 429", got)
	}
	if got.Error != "health probe returned HTTP 429 (app_concurrency_reached)" {
		t.Fatalf("error = %q", got.Error)
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
