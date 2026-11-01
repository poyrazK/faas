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

// adr: 091 — amendment 2026-10-31 (security review): the gateway applies an
// account's edge rules to every host they cover, so a deploy token, which
// deploys one app, cannot write a rule that covers the account's other apps.
func TestDeployTokenEdgeRulesStayOnItsApp(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "edge-token-a")
	apidserver.MustSeedApp(t, e, "edge-token-b")
	createdRec := e.Do(t, http.MethodPost, "/v1/apps/edge-token-a/deploy-tokens", api.CreateDeployTokenRequest{Label: "ci-a"}, nil)
	if createdRec.Code != http.StatusCreated {
		t.Fatalf("create token: code=%d body=%s", createdRec.Code, createdRec.Body.String())
	}
	var token api.DeployTokenResponse
	if err := json.Unmarshal(createdRec.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	create := func(host string) *httptest.ResponseRecorder {
		t.Helper()
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(api.CreateEdgeRuleRequest{
			MatchHost: host, MatchPath: "/", Kind: "ip", Action: json.RawMessage(`{"deny":["203.0.113.0/24"]}`),
		}); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/apps/edge-token-a/edge-rules", &buf)
		req.Header.Set("Authorization", "Bearer "+token.Plaintext)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		return rec
	}
	for _, host := range []string{"*", "*.example.com", "edge-token-b.example.com"} {
		if rec := create(host); rec.Code != http.StatusForbidden {
			t.Fatalf("deploy token rule on %q = %d %s, want 403", host, rec.Code, rec.Body.String())
		}
	}
	if rec := create("@app"); rec.Code != http.StatusCreated {
		t.Fatalf("deploy token rule on its own app = %d %s, want 201", rec.Code, rec.Body.String())
	}
	// The account's own session still writes a rule for every host.
	if rec := e.Do(t, http.MethodPost, "/v1/apps/edge-token-a/edge-rules", api.CreateEdgeRuleRequest{
		MatchHost: "*", MatchPath: "/", Kind: "ip", Action: json.RawMessage(`{"deny":["198.51.100.0/24"]}`),
	}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("session rule on every host = %d %s, want 201", rec.Code, rec.Body.String())
	}
}
