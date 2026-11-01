package apidserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// "Sign out other devices" ends the keys an interactive sign-in minted as
// well as the dashboard sessions: a 30-day cli-login or mcp-login key minted
// with a stolen session used to survive it, and the password reset (bug hunt
// 2026-10-28 H71). Keys the customer created on purpose stay.
func TestRevokeAllSessionsRevokesInteractiveSignInKeys(t *testing.T) {
	env := newSessionEnv(t)
	ctx := context.Background()
	ids := map[string]string{}
	for _, label := range []string{"signup", "cli-login", api.MCPLoginKeyLabel, "ci-deploy"} {
		key, err := env.store.CreateAPIKey(ctx, env.acct.ID, []byte("hash-"+label), label, api.ScopesAdminOnly)
		if err != nil {
			t.Fatal(err)
		}
		ids[label] = key.ID
	}
	if rec := doSession(t, env, http.MethodPost, "/v1/auth/sessions/revoke_all", nil, env.cookie); rec.Code != http.StatusOK {
		t.Fatalf("revoke_all = %d: %s", rec.Code, rec.Body.String())
	}
	for label, id := range ids {
		key, err := env.store.GetAPIKey(ctx, env.acct.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		if revoked, want := key.Status == "revoked", label != "ci-deploy"; revoked != want {
			t.Errorf("%s key status = %q, want revoked=%v", label, key.Status, want)
		}
	}
}
