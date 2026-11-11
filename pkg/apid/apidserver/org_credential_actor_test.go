// adr: 20261110190000
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// orgKey mints an organization key held by accountID and returns it with its
// bearer header.
func orgKey(t *testing.T, store state.Store, orgID, accountID, label string) (state.APIKey, map[string]string) {
	t.Helper()
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateOrgAPIKey(t.Context(), orgID, accountID, hash, label, api.ScopesAdminOnly, nil)
	if err != nil {
		t.Fatal(err)
	}
	return key, map[string]string{"Authorization": "Bearer " + plain}
}

// Rotating a key returns the new key's plaintext and the new key keeps the
// old holder, so an admin rotating the owner's key would receive a key that
// acts as the owner. Only the holder rotates; admins still revoke.
func TestOrgKeyRotationIsLimitedToTheHolder(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, owner := *e.Store(), *e.Acct()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "rotate-holder", "Rotate Holder", api.PlanPro)
	ownerKey, _ := orgKey(t, store, org.ID, owner.ID, "owner-ci")
	admin, err := store.CreateAccount(t.Context(), "rotate-admin@team.example", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), org.ID, admin.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	adminKey, adminAuth := orgKey(t, store, org.ID, admin.ID, "admin-ci")
	keys := "/v1/orgs/" + org.Slug + "/keys/"

	if rec := e.Do(t, http.MethodPost, keys+ownerKey.ID+"/rotate", api.RotateOrgAPIKeyRequest{}, adminAuth); rec.Code != http.StatusForbidden {
		t.Fatalf("admin rotating the owner's key = %d %s, want 403", rec.Code, rec.Body)
	}
	if k, err := store.GetOrgAPIKey(t.Context(), org.ID, ownerKey.ID); err != nil || k.Status != string(state.APIKeyStatusActive) {
		t.Fatalf("owner key after refused rotation = %+v %v, want still active", k, err)
	}
	rec := e.Do(t, http.MethodPost, keys+adminKey.ID+"/rotate", api.RotateOrgAPIKeyRequest{}, adminAuth)
	var rotated api.RotateOrgAPIKeyResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &rotated) != nil {
		t.Fatalf("admin rotating their own key = %d %s, want 200", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodDelete, keys+ownerKey.ID, nil, adminAuth); rec.Code != http.StatusNoContent {
		t.Fatalf("admin revoking the owner's key = %d %s, want 204", rec.Code, rec.Body)
	}
}

// A key bound to a company organization cannot manage its holder's personal
// account, keys or sessions; it still reads who it is.
func TestOrgKeyRefusedOnPersonalIdentityRoutes(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, owner := *e.Store(), *e.Acct()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "personal-fence", "Personal Fence", api.PlanPro)
	_, companyAuth := orgKey(t, store, org.ID, owner.ID, "company-ci")
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/v1/keys"},
		{http.MethodGet, "/v1/keys"},
		{http.MethodGet, "/v1/account/export"},
		{http.MethodDelete, "/v1/account"},
		{http.MethodPatch, "/v1/account"},
		{http.MethodPost, "/v1/account/restore"},
		{http.MethodPost, "/v1/account/mfa/disable"},
		{http.MethodGet, "/v1/auth/sessions"},
	} {
		if rec := e.Do(t, tc.method, tc.path, nil, companyAuth); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with a company key = %d %s, want 403", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
	if rec := e.Do(t, http.MethodGet, "/v1/account", nil, companyAuth); rec.Code != http.StatusOK {
		t.Errorf("GET /v1/account with a company key = %d %s, want 200", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/keys", nil, nil); rec.Code != http.StatusOK {
		t.Errorf("GET /v1/keys with a personal key = %d %s, want 200", rec.Code, rec.Body)
	}
}

// A deploy token minted by an admin on the owner's company app acts as the
// admin and stops working when the admin is removed.
func TestCompanyDeployTokenActsAsItsMinter(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "token-minter", "Token Minter", api.PlanPro)
	if rec := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "minter-api"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create company app: %d %s", rec.Code, rec.Body)
	}
	admin, err := store.CreateAccount(t.Context(), "minter-admin@team.example", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), org.ID, admin.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	_, adminAuth := orgKey(t, store, org.ID, admin.ID, "admin-ci")
	rec := e.Do(t, http.MethodPost, "/v1/apps/minter-api/deploy-tokens", api.CreateDeployTokenRequest{Label: "admin-ci"}, adminAuth)
	var created api.DeployTokenResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &created) != nil || created.Plaintext == "" {
		t.Fatalf("admin mints a token: %d %s", rec.Code, rec.Body)
	}
	token := map[string]string{"Authorization": "Bearer " + created.Plaintext}
	if rec := e.Do(t, http.MethodGet, "/v1/apps/minter-api", nil, token); rec.Code != http.StatusOK {
		t.Fatalf("token while the admin is a member = %d %s, want 200", rec.Code, rec.Body)
	}
	if err := store.RemoveOrgMember(t.Context(), org.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/apps/minter-api", nil, token); rec.Code == http.StatusOK {
		t.Fatalf("token after the admin's removal = %d %s, want refused", rec.Code, rec.Body)
	}
}
