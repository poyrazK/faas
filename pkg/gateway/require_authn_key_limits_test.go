// adr: 20261104085712
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// limitedKeyAuthn resolves every token to one fixed key on the app's account.
type limitedKeyAuthn struct {
	accountID string
	key       RequireAuthnKey
}

func (f limitedKeyAuthn) AuthenticateKey(context.Context, []byte) (RequireAuthnAccount, RequireAuthnKey, error) {
	return RequireAuthnAccount{ID: f.accountID}, f.key, nil
}

// A key bound to another app, or limited to projects the app is not in,
// does not unlock a private app, as on the API (ADR-20261104085712).
func TestRequireAuthnHonoursKeyAppAndProjectLimits(t *testing.T) {
	read := []string{api.ScopeAgent, api.ScopeAppsRead}
	for _, tc := range []struct {
		name       string
		appProject string
		key        RequireAuthnKey
		want       int
	}{
		{"unlimited key", "proj-a", RequireAuthnKey{ID: "k", Scopes: read}, http.StatusOK},
		{"key bound to this app", "", RequireAuthnKey{ID: "k", Scopes: read, AppID: "app-1"}, http.StatusOK},
		{"key bound to another app", "", RequireAuthnKey{ID: "k", Scopes: read, AppID: "app-2"}, http.StatusForbidden},
		{"limited to the app's project", "proj-a", RequireAuthnKey{ID: "k", Scopes: read, ProjectLimited: true, ProjectIDs: []string{"proj-a"}}, http.StatusOK},
		{"limited to another project", "proj-b", RequireAuthnKey{ID: "k", Scopes: read, ProjectLimited: true, ProjectIDs: []string{"proj-a"}}, http.StatusForbidden},
		{"limited key, app in no project", "", RequireAuthnKey{ID: "k", Scopes: read, ProjectLimited: true, ProjectIDs: []string{"proj-a"}}, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, b, _, audit, _ := newRequireAuthnTestHandler(t, true, "acct-A")
			b.app.ProjectID = tc.appProject
			h.WithRequireAuthn(limitedKeyAuthn{accountID: "acct-A", key: tc.key}, audit)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, reqFor(t, "Bearer "+validKeyFormat("000000000000000000000000000000000000000000000001")))
			// An admitted key passes the authz branch; what follows (a
			// project app's release resolution in this fake) is not under
			// test, so only a refusal is pinned exactly.
			if refused := rec.Code == http.StatusForbidden; refused != (tc.want == http.StatusForbidden) {
				t.Fatalf("status = %d, want refused=%v (body %s)", rec.Code, tc.want == http.StatusForbidden, rec.Body)
			}
			if tc.want == http.StatusForbidden && audit.counts["instances.authn_scope"] != 1 {
				t.Fatalf("authn_scope audit count = %d, want 1", audit.counts["instances.authn_scope"])
			}
		})
	}
}
