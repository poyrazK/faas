// adr: 20261103172551
package gateway

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func basicAuthReqFrom(t *testing.T, ip, user, pass string) *http.Request {
	t.Helper()
	req := publicAuthReqFor(t, "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	req.Header.Set("X-Forwarded-For", ip)
	return req
}

// A client that keeps presenting wrong basic-auth credentials is refused
// with 429 once it spends its budget, even with the right password, while
// other clients are unaffected (ADR-20261103172551).
func TestPublicAuthBasicThrottlesWrongCredentialsPerClient(t *testing.T) {
	t.Parallel()
	h, _, _, _, audit := newPublicAuthTestHandler(t, "basic", "acct-1")
	for i := range api.GatewayFailedBasicAuthPerSourcePerApp {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, basicAuthReqFrom(t, "198.51.100.9", "alice", "guess"))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("guess %d: status = %d, want 401", i, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, basicAuthReqFrom(t, "198.51.100.9", "alice", "s3cret"))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("past the budget: status = %d, want 429", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("429 has no Retry-After")
	}
	if audit.counts["instances.public_auth_throttled"] != 1 {
		t.Errorf("throttled audit count = %d, want 1", audit.counts["instances.public_auth_throttled"])
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, basicAuthReqFrom(t, "198.51.100.10", "alice", "s3cret"))
	if rr.Code != http.StatusOK {
		t.Fatalf("another client: status = %d, want 200", rr.Code)
	}
}

// Successful sign-ins do not spend the budget.
func TestPublicAuthBasicSuccessDoesNotSpendBudget(t *testing.T) {
	t.Parallel()
	h, _, _, _, _ := newPublicAuthTestHandler(t, "basic", "acct-1")
	for i := range api.GatewayFailedBasicAuthPerSourcePerApp + 5 {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, basicAuthReqFrom(t, "198.51.100.9", "alice", "s3cret"))
		if rr.Code != http.StatusOK {
			t.Fatalf("sign-in %d: status = %d, want 200", i, rr.Code)
		}
	}
}

// The budget is per app: one app's lockout does not lock the client out of
// another app.
func TestBasicAuthFailureKeyIsPerApp(t *testing.T) {
	req := httptest.NewRequest("GET", "http://app.example.test/", nil)
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	a, b := basicAuthFailureKey(req, "app-a"), basicAuthFailureKey(req, "app-b")
	if a == "" || a == b {
		t.Fatalf("keys = %q, %q, want distinct non-empty keys", a, b)
	}
	if got := basicAuthFailureKey(httptest.NewRequest("GET", "http://app.example.test/", nil), "app-a"); got != "" {
		t.Fatalf("key without a trusted client address = %q, want empty", got)
	}
}
