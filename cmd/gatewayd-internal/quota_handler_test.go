// Tests for /v1/internal/quota — Finding 6 (issue #314).
//
// The handler reads the per-app rate-limit bucket for (plan, app_id)
// and returns the same QuotaSnapshot the response-header writer
// emits. Tests pin the wire shape (JSON + X-Faas-Quota-State header),
// the validation contract (missing params, unknown plan), and the
// noop gate (limiter not wired → 503 with X-Faas-Quota-State: noop).
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// quotaUnwiredBackend routes nothing; every request 404s. Used by the
// /v1/internal/quota tests so the handler has a Backend to call into
// without touching the real Postgres path. PR A (pure file moves) —
// the pre-split cmd/gatewayd/main.go hosted the same shim under the
// name unwiredBackend. We re-declare it locally because the Handler
// constructor requires a Backend value and the quota tests don't care
// which one.
type quotaUnwiredBackend struct{}

func (quotaUnwiredBackend) Lookup(_ context.Context, _ string) (gateway.App, bool) {
	return gateway.App{}, false
}
func (quotaUnwiredBackend) Pick(_ string) (gateway.Target, bool) {
	return gateway.Target{}, false
}
func (quotaUnwiredBackend) HealthyCount(_ string) int { return 0 }
func (quotaUnwiredBackend) Admit(_ context.Context, _ string, _ int) (string, gateway.WakeMethod, bool, error) {
	return "", gateway.WakeMethodUnspecified, false, nil
}

// newQuotaTestHandler wires a *gateway.Handler with a fresh *Limiter
// so the test can drain the bucket and verify the snapshot reads
// the same value.
func newQuotaTestHandler() *gateway.Handler {
	return gateway.NewHandlerWith(quotaUnwiredBackend{}, nil, nil) // metrics=nil → no-op observation
}

func TestInternalQuotaHandler_MissingParam_400(t *testing.T) {
	h := newQuotaTestHandler()
	tests := map[string]string{
		"missing both":   "/v1/internal/quota",
		"missing app_id": "/v1/internal/quota?plan=pro",
		"missing plan":   "/v1/internal/quota?app_id=abc",
		"empty both":     "/v1/internal/quota?plan=&app_id=",
	}
	for name, target := range tests {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", target, nil)
			internalQuotaHandler(h, nil).ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s: got %d; want 400", target, rec.Code)
			}
			if !strings.Contains(rec.Header().Get("Content-Type"), "application/problem+json") {
				t.Errorf("Content-Type missing problem+json: %q", rec.Header().Get("Content-Type"))
			}
		})
	}
}

func TestInternalQuotaHandler_UnknownPlan_400(t *testing.T) {
	h := newQuotaTestHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/internal/quota?plan=enterprise&app_id=app-1", nil)
	internalQuotaHandler(h, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown plan: got %d; want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unknown_plan") {
		t.Errorf("problem code missing: %s", rec.Body.String())
	}
}

func TestInternalQuotaHandler_FreshBucket_Noop(t *testing.T) {
	// Bucket has never been Allow'd for (app-1, hobby) — Peek returns
	// ok=false and the handler emits ok=false with X-Faas-Quota-State:
	// noop (still 200 OK so the dashboard JS doesn't crash).
	h := newQuotaTestHandler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/internal/quota?plan=hobby&app_id=app-1", nil)
	internalQuotaHandler(h, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("fresh bucket: got %d; want 200 (ok=false is not a failure)", rec.Code)
	}
	if got := rec.Header().Get("X-Faas-Quota-State"); got != "noop" {
		t.Errorf("X-Faas-Quota-State = %q; want noop", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"ok":false`) {
		t.Errorf("body missing ok=false: %s", body)
	}
}

// TestInternalQuotaHandler_AfterAllow — limit + remaining match what
// Limiter.Peek would have produced for the same (plan, app_id). The
// dashboard reads this and renders it; the value contract is the
// load-bearing wire shape.
func TestInternalQuotaHandler_AfterAllow(t *testing.T) {
	h := gateway.NewHandlerWith(quotaUnwiredBackend{}, nil, nil)
	if !h.Limiter().Allow("app-1", api.PlanHobby) {
		t.Fatal("Allow returned false; want true")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/internal/quota?plan=hobby&app_id=app-1", nil)
	internalQuotaHandler(h, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("got %d; want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Faas-Quota-State"); got != "ok" {
		t.Errorf("X-Faas-Quota-State = %q; want ok", got)
	}
	body := rec.Body.String()
	// Hobby plan: RateLimitBurst = 100, RateLimitRPS = 20. After one
	// Allow, remaining = 99.
	for _, want := range []string{
		`"app_id":"app-1"`,
		`"plan":"hobby"`,
		`"limit":100`,
		`"remaining":99`,
		`"reset_seconds":0`,
		`"ok":true`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s in:\n%s", want, body)
		}
	}
}

func TestQuotaParsePlan(t *testing.T) {
	tests := map[string]api.Plan{
		"free":    api.PlanFree,
		"hobby":   api.PlanHobby,
		"pro":     api.PlanPro,
		"scale":   api.PlanScale,
		"":        api.Plan(""),
		"unknown": api.Plan(""),
	}
	for in, want := range tests {
		got := quotaParsePlan(in)
		if got != want {
			t.Errorf("quotaParsePlan(%q) = %q; want %q", in, got, want)
		}
	}
}

// TestInternalQuotaHandler_EscapesUserSuppliedValues is the
// tripwire for CodeQL alert #146 (go/reflected-xss). The endpoint
// is loopback-only and the live caller is an operator's curl or a
// future apid-side dial that JSON.parses the response — neither
// path renders raw response text as HTML — but the test pins the
// defense-in-depth guarantee that user-supplied app_id/plan
// values are escaped per RFC 8259 §7 in the JSON body. If a
// future PR reverts to string-concat, the raw payload leaks and
// this test fails on the literal `<` / `>` / `</script>` check.
func TestInternalQuotaHandler_EscapesUserSuppliedValues(t *testing.T) {
	h := newQuotaTestHandler()
	// Payload chosen to fail loudly if it lands raw in the body:
	// would close the surrounding <script> tag and inject a payload
	// that fires on page load.
	evil := `</script><script>alert(1)</script>`
	req := httptest.NewRequest("GET",
		"/v1/internal/quota?plan=hobby&app_id="+url.QueryEscape(evil), nil)
	rec := httptest.NewRecorder()
	internalQuotaHandler(h, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d; want 200", rec.Code)
	}
	body := rec.Body.String()

	// Negative assertions: the raw payload must NOT appear in
	// the body. json.Marshal escapes U+003C / U+003E per RFC 8259
	// §7 so the literal `<`, `>`, `<script>`, `</script>` and the
	// full payload are absent.
	for _, raw := range []string{evil, "<script>", "</script>", "<", ">"} {
		if strings.Contains(body, raw) {
			t.Errorf("raw payload %q leaked into body; body = %s", raw, body)
		}
	}

	// Positive assertion: the JSON-string must be parseable, and
	// the round-tripped value must equal the original payload. This
	// pins the defense-in-depth without dragging the 6-byte ASCII
	// escape sequences through the test source (the escapes are
	// easy to typo and the round-trip test exercises the same
	// production code path).
	var got struct {
		AppID string `json:"app_id"`
		Plan  string `json:"plan"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("body is not valid JSON: %v; body = %s", err, body)
	}
	if got.AppID != evil {
		t.Errorf("round-trip app_id = %q; want %q", got.AppID, evil)
	}
	if got.Plan != "hobby" {
		t.Errorf("plan = %q; want hobby", got.Plan)
	}
}
