package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// humanLoginKey mints the key `gregale login` gives a person, which may
// approve a protected environment (bug hunt H70).
func humanLoginKey(t *testing.T, store state.Store, accountID string) string {
	t.Helper()
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), accountID, hash, "cli-login", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	return plain
}

// humanOrgLoginHeaders is humanLoginKey for an organization member.
func humanOrgLoginHeaders(t *testing.T, store state.Store, orgID, accountID string) map[string]string {
	t.Helper()
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateOrgAPIKey(t.Context(), orgID, accountID, hash, "cli-login", api.ScopesAdminOnly, nil); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"Authorization": "Bearer " + plain}
}

// A customer cannot give a CI key a label that marks a person's sign-in,
// which protected-environment approvals trust (bug hunt H70).
func TestCreateKeyRefusesInteractiveSignInLabels(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	for _, label := range []string{"cli-login", "signup", "mcp-login", " cli-login "} {
		rec := e.Do(t, http.MethodPost, "/v1/keys", api.CreateKeyRequest{Label: label, Scopes: []string{api.ScopeDeployWrite}}, nil)
		apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	}
	if rec := e.Do(t, http.MethodPost, "/v1/keys", api.CreateKeyRequest{Label: "ci", Scopes: []string{api.ScopeDeployWrite}}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("ordinary label refused: %d %s", rec.Code, rec.Body)
	}
}
