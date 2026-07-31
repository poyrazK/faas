// Tests for /v1/internal/quota — Finding 6 (issue #314).
//
// The handler reads the per-app rate-limit bucket for (plan, app_id)
// and returns the same QuotaSnapshot the response-header writer
// emits. Tests pin the wire shape (JSON + X-Faas-Quota-State header),
// the validation contract (missing params, unknown plan), and the
// noop gate (limiter not wired → 503 with X-Faas-Quota-State: noop).
package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// newQuotaTestHandler wires a *gateway.Handler with a fresh *Limiter
// so the test can drain the bucket and verify the snapshot reads
// the same value.
func newQuotaTestHandler() *gateway.Handler {
	return gateway.NewHandlerWith(unwiredBackend{}, nil, nil) // metrics=nil → no-op observation
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
	h := gateway.NewHandlerWith(unwiredBackend{}, nil, nil)
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
