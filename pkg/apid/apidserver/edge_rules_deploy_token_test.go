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
	// A route rule on the token's own hosts still may not serve another app
	// there (ADR-091 amendment 2026-11-02, review round 8).
	route := func(target string) *httptest.ResponseRecorder {
		t.Helper()
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(api.CreateEdgeRuleRequest{
			MatchHost: "@app", MatchPath: "/b/**", Kind: "route", Action: json.RawMessage(`{"target_app_slug":"` + target + `"}`),
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
	for _, target := range []string{"edge-token-b", "no-such-app-here"} {
		if rec := route(target); rec.Code != http.StatusForbidden {
			t.Fatalf("deploy token route to %q = %d %s, want 403", target, rec.Code, rec.Body.String())
		}
	}
	if rec := route("edge-token-a"); rec.Code != http.StatusCreated {
		t.Fatalf("deploy token route to its own app = %d %s, want 201", rec.Code, rec.Body.String())
	}
	siblingRec := e.Do(t, http.MethodPost, "/v1/apps/edge-token-a/edge-rules", api.CreateEdgeRuleRequest{
		MatchHost: "@app", MatchPath: "/b/**", Kind: "route", Action: json.RawMessage(`{"target_app_slug":"edge-token-b"}`),
	}, nil)
	if siblingRec.Code != http.StatusCreated {
		t.Fatalf("session route to a sibling app = %d %s, want 201", siblingRec.Code, siblingRec.Body.String())
	}
	var sibling api.EdgeRuleResponse
	if err := json.Unmarshal(siblingRec.Body.Bytes(), &sibling); err != nil {
		t.Fatal(err)
	}
	// Nor may it widen or re-enable that account route without touching its
	// action (amendment 2026-11-03, review round 9).
	patch := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/v1/edge-rules/"+sibling.ID, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer "+token.Plaintext)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		return rec
	}
	for _, body := range []string{`{"match_path":"/**"}`, `{"enabled":true}`, `{"match_methods":["GET"]}`} {
		if rec := patch(body); rec.Code != http.StatusForbidden {
			t.Fatalf("deploy token PATCH %s of a sibling route = %d %s, want 403", body, rec.Code, rec.Body.String())
		}
	}
	// The account's own session still writes a rule for every host.
	if rec := e.Do(t, http.MethodPost, "/v1/apps/edge-token-a/edge-rules", api.CreateEdgeRuleRequest{
		MatchHost: "*", MatchPath: "/", Kind: "ip", Action: json.RawMessage(`{"deny":["198.51.100.0/24"]}`),
	}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("session rule on every host = %d %s, want 201", rec.Code, rec.Body.String())
	}
}
