package gateway

import (
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/internalsvc"
)

// Every synth route requires a schedd token minted for the app it names,
// whatever the app's public auth mode: gatewayd-public forwards public
// traffic into the synth listener (ADR-20261103090000).
func TestRequireSchedulerToken(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	mint := func(svc, appID string) string {
		tok, err := internalsvc.Mint(svc, 30*time.Second, map[string]any{"app_id": appID}, priv, "")
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	for _, tc := range []struct {
		name, auth, reason string
	}{
		{name: "scheduler token for the app", auth: "Bearer " + mint("schedd", "app-1")},
		{name: "no token", reason: "missing_authorization_header"},
		{name: "token for another app", auth: "Bearer " + mint("schedd", "app-2"), reason: "app_mismatch"},
		{name: "another service", auth: "Bearer " + mint("apid", "app-1"), reason: "not_scheduler"},
		{name: "garbage", auth: "Bearer not-a-token", reason: "invalid_token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, audit, _ := newTestSynthServerForInternalSvc(t, map[string]ed25519.PublicKey{"schedd": pub, "apid": pub})
			req := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			rec := httptest.NewRecorder()
			refused := srv.requireSchedulerToken(rec, req, "app-1", "synth_dispatch")
			if refused != (tc.reason != "") {
				t.Fatalf("refused = %v, want %v", refused, tc.reason != "")
			}
			if !refused {
				return
			}
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
			if len(audit.events) != 1 || audit.events[0].kind != "instances.synth_unauthorized" || audit.events[0].data["reason"] != tc.reason {
				t.Fatalf("audit = %+v, want one synth_unauthorized with reason %s", audit.events, tc.reason)
			}
		})
	}
}

// The routes refuse before they look up the app's policy or wake anything:
// the test server has no dispatcher and no policy lookup to reach.
func TestSynthRoutesRefuseWithoutSchedulerToken(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		"/v1/synthesize":                 `{"app_id":"app-1","path":"/"}`,
		"/v1/invocations:dispatch":       `{"app_id":"app-1","invocation_id":"inv-1","source":"async_invoke","path":"/admin"}`,
		"/v1/invocations:dispatch_batch": `{"app_id":"app-1","invocation_id":"inv-1","source":"queue","records":[]}`,
	} {
		t.Run(path, func(t *testing.T) {
			srv, _, _ := newTestSynthServerForInternalSvc(t, map[string]ed25519.PublicKey{"schedd": pub})
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			rec := httptest.NewRecorder()
			switch path {
			case "/v1/synthesize":
				srv.handleSynthesize(rec, req)
			case "/v1/invocations:dispatch":
				srv.handleInvocationDispatch(rec, req)
			default:
				srv.handleInvocationDispatchBatch(rec, req)
			}
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d (%s), want 403", rec.Code, rec.Body.String())
			}
		})
	}
}
