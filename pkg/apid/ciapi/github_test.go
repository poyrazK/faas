package ciapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/apid/ciapi"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCIGitHubBindingRequiresVerifiedRepositoryAndKeepsPlanPinned(t *testing.T) {
	account, other := uuid.NewString(), uuid.NewString()
	s := ci.NewMemoryStore()
	def := ci.Definition{Version: 1, Repository: "gregale/api", Jobs: map[string]ci.Job{"test": {Image: "registry.example/ci@sha256:" + strings.Repeat("a", 64), Steps: []ci.Step{{Name: "unit", Run: "stored only"}}}}}
	r, err := s.PutRevision(t.Context(), account, "checks", def)
	if err != nil {
		t.Fatal(err)
	}
	cfg := ci.ConfigFromEnv(func(key string) string {
		if key == ci.AccountsEnv {
			return account
		}
		if key == ci.InternalEnv || key == ci.GitHubEnv {
			return "1"
		}
		return ""
	})
	verified := 0
	h := ciapi.Handler{Config: cfg, Store: s, VerifyGitHub: func(_ context.Context, acct string, installation, repository int64, name string) error {
		verified++
		if acct != account || installation != 41 || repository != 123 || name != "gregale/api" {
			return ci.ErrGitHubAccess
		}
		return nil
	}}
	body, _ := json.Marshal(ci.GitHubBindingRequest{RevisionID: r.ID, InstallationID: 41, RepositoryID: 123, PushBranches: []string{"main"}})
	call := func(acct string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/v1/internal/ci/github/bindings/checks", strings.NewReader(string(body)))
		req.SetPathValue("name", "checks")
		w := httptest.NewRecorder()
		h.PutGitHubBinding(w, req, state.Account{ID: acct})
		return w
	}
	if got := call(other); got.Code != http.StatusNotFound || verified != 0 {
		t.Fatal("unlisted account reached repository authorization")
	}
	if got := call(account); got.Code != http.StatusOK || verified != 1 {
		t.Fatalf("bind failed: %d %s", got.Code, got.Body)
	}
	h.VerifyGitHub = func(context.Context, string, int64, int64, string) error { return ci.ErrGitHubAccess }
	if got := call(account); got.Code != http.StatusForbidden {
		t.Fatal("unverified repository accepted")
	}
	rows, err := s.GitHubBindings(t.Context(), account)
	if err != nil || len(rows) != 1 || rows[0].RevisionID != r.ID {
		t.Fatal("binding mutation escaped account/revision boundary")
	}
}
