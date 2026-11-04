package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// rc.318 field report: an app deploy token could `deploy --yes` but not
// `deploy --dry-run`, because the diff route took only apps:read. It now
// previews its own app's deploy, and still nothing else.
func TestDeployTokenDiffsOnlyItsApp(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "diff-token-a")
	apidserver.MustSeedApp(t, e, "diff-token-b")
	createdRec := e.Do(t, http.MethodPost, "/v1/apps/diff-token-a/deploy-tokens", api.CreateDeployTokenRequest{Label: "ci-a"}, nil)
	if createdRec.Code != http.StatusCreated {
		t.Fatalf("create token: code=%d body=%s", createdRec.Code, createdRec.Body.String())
	}
	var token api.DeployTokenResponse
	if err := json.Unmarshal(createdRec.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	diff := func(slug string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/apps/"+slug+"/diff", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Authorization", "Bearer "+token.Plaintext)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		return rec
	}
	rec := diff("diff-token-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("deploy token diff of its app = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var resp api.DiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Diff.Slug != "diff-token-a" {
		t.Fatalf("diff response = %+v (%v); body=%s", resp, err, rec.Body.String())
	}
	// The binding answers as for an app the token cannot see.
	for _, slug := range []string{"diff-token-b", "diff-token-unknown"} {
		if rec := diff(slug); rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
			t.Fatalf("deploy token diff of %s = %d %s, want 403 or 404", slug, rec.Code, rec.Body.String())
		}
	}
}
