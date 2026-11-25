package gateway

// adr: 20261124094745 — verified MCP OAuth callers become the app's API
// consumers through apid, cached; refusals fail closed.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
)

type fakeOAuthEnroller struct {
	calls                  int
	issuer, subject, email string
	id                     string
	active                 bool
	err                    error
}

func (f *fakeOAuthEnroller) EnrollOAuthConsumer(_ context.Context, _, _, issuer, subject, email string) (string, bool, error) {
	f.calls++
	f.issuer, f.subject, f.email = issuer, subject, email
	return f.id, f.active, f.err
}

func TestMCPOAuthConsumerEnrollment(t *testing.T) {
	p := &api.MCPResourcePolicy{Resource: "https://app.example/mcp", Scopes: []string{"mcp:read"}, ToolScopes: map[string][]string{"read": {}}}
	rule := &EdgeRuleJWTResolved{Issuer: "https://issuer.example", MCP: p}
	app := App{ID: "app-1", AccountID: "acct-1"}
	call := func(h *Handler, subject string) (*httptest.ResponseRecorder, *http.Request, bool) {
		r := httptest.NewRequest("POST", "https://app.example/mcp", strings.NewReader(`{"method":"tools/call","params":{"name":"read"}}`))
		r.Header.Set("Authorization", "Bearer jwt")
		w := httptest.NewRecorder()
		return w, r, h.applyMCPResourcePolicy(w, r, app, rule)
	}
	handler := func(e *fakeOAuthEnroller, subject string) *Handler {
		h := &Handler{apiMonetization: monetizationOn, jwtVerifier: &countingJWTVerifier{onVerify: func(context.Context, string, *EdgeRuleJWTResolved) (*JWTClaims, error) {
			return &JWTClaims{Subject: subject, Exp: time.Now().Add(time.Minute), Custom: map[string]string{"scope": "mcp:read", "email": "ada@example.com"}}, nil
		}}}
		return h.WithOAuthConsumers(e)
	}

	enroller := &fakeOAuthEnroller{id: "consumer-1", active: true}
	h := handler(enroller, "u1")
	for range 2 {
		w, r, handled := call(h, "u1")
		if handled || authenticatedFrom(r.Context()).ConsumerID != "consumer-1" {
			t.Fatalf("handled=%v consumer=%q body=%s", handled, authenticatedFrom(r.Context()).ConsumerID, w.Body)
		}
	}
	if enroller.calls != 1 || enroller.issuer != "https://issuer.example" || enroller.subject != "u1" || enroller.email != "ada@example.com" {
		t.Fatalf("enroller = %+v; want one cached call", enroller)
	}

	// An app that does not enroll callers serves them unattributed, and
	// is asked once for any caller.
	off := &fakeOAuthEnroller{err: apidgrpc.ErrOAuthConsumersOff}
	h = handler(off, "u1")
	for range 2 {
		if _, r, handled := call(h, "u1"); handled || authenticatedFrom(r.Context()).ConsumerID != "" {
			t.Fatal("off: caller refused or attributed")
		}
	}
	if off.calls != 1 {
		t.Fatalf("off asked %d times", off.calls)
	}

	for name, tc := range map[string]struct {
		enroller *fakeOAuthEnroller
		status   int
		code     string
	}{
		"revoked":     {&fakeOAuthEnroller{id: "consumer-1"}, http.StatusForbidden, "mcp_access_denied"},
		"daily limit": {&fakeOAuthEnroller{err: apidgrpc.ErrOAuthConsumerSignupLimit}, http.StatusTooManyRequests, api.CodeConsumerSignupDailyLimit},
		// A permanent refusal (free plan gone) is never served unattributed,
		// whether or not this gateway saw the app enroll anyone.
		"refused": {&fakeOAuthEnroller{err: apidgrpc.ErrOAuthConsumerRefused}, http.StatusForbidden, "mcp_access_denied"},
	} {
		w, _, handled := call(handler(tc.enroller, "u1"), "u1")
		if !handled || w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Errorf("%s: handled=%v status=%d body=%s", name, handled, w.Code, w.Body)
		}
	}

	// apid down: an app not known to enroll callers keeps serving them
	// unattributed; one that enrolled a caller fails closed.
	down := &fakeOAuthEnroller{err: errors.New("unavailable")}
	if _, r, handled := call(handler(down, "u1"), "u1"); handled || authenticatedFrom(r.Context()).ConsumerID != "" {
		t.Fatal("apid down: an app not known to enroll callers was refused")
	}
	known := &fakeOAuthEnroller{id: "consumer-1", active: true}
	h = handler(known, "u1")
	call(h, "u1")
	known.err = errors.New("unavailable")
	h.jwtVerifier = &countingJWTVerifier{onVerify: func(context.Context, string, *EdgeRuleJWTResolved) (*JWTClaims, error) {
		return &JWTClaims{Subject: "u2", Exp: time.Now().Add(time.Minute), Custom: map[string]string{"scope": "mcp:read"}}, nil
	}}
	if w, _, handled := call(h, "u2"); !handled || w.Code != http.StatusServiceUnavailable ||
		!strings.Contains(w.Body.String(), api.CodeConsumerEnrollmentUnavailable) {
		t.Fatalf("apid down for an enrolling app: handled=%v status=%d body=%s", handled, w.Code, w.Body)
	}

	// Without API monetization nothing is enrolled.
	quiet := &fakeOAuthEnroller{id: "consumer-1", active: true}
	h = handler(quiet, "u1")
	h.apiMonetization = func() bool { return false }
	if _, r, handled := call(h, "u1"); handled || authenticatedFrom(r.Context()).ConsumerID != "" || quiet.calls != 0 {
		t.Fatal("enrolled while monetization is off")
	}
}
