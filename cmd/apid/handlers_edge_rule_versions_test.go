package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-831 §2 end to end over HTTP: every change records a version, the
// listing carries an ETag, a stale If-Match is refused with 412 without
// touching the rules, and rollback restores an earlier set as a new version.
func TestEdgeRuleSetVersions_IfMatchAndRollback(t *testing.T) {
	e := setup(t, api.PlanHobby)
	slug := mustSeedEdgeRuleApp(t, e, "versions")

	first := edgeRuleRouteReq("legacy")
	first.MatchHost = "a.example.com"
	first.Name = "first"
	rec := e.do(t, "POST", "/v1/apps/"+slug+"/edge-rules", first, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create first: %d %s", rec.Code, rec.Body.String())
	}
	var created api.EdgeRuleResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if got := rec.Header().Get("ETag"); got != `"1"` {
		t.Fatalf("create ETag = %q, want \"1\"", got)
	}

	second := edgeRuleRouteReq("legacy")
	second.MatchHost = "b.example.com"
	if rec := e.do(t, "POST", "/v1/apps/"+slug+"/edge-rules", second, map[string]string{"If-Match": `"1"`}); rec.Code != http.StatusCreated {
		t.Fatalf("create with current If-Match: %d %s", rec.Code, rec.Body.String())
	}

	// A writer still holding version 1 is refused and changes nothing.
	stale := map[string]string{"If-Match": `"1"`}
	rec = e.do(t, "DELETE", "/v1/edge-rules/"+created.ID, nil, stale)
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match delete: %d %s, want 412", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("ETag"); got != `"2"` {
		t.Fatalf("412 ETag = %q, want the current \"2\"", got)
	}
	if rec := e.do(t, "GET", "/v1/edge-rules/"+created.ID, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("rule deleted despite the failed precondition: %d", rec.Code)
	}

	rec = e.do(t, "GET", "/v1/apps/"+slug+"/edge-rules/versions", nil, nil)
	var versions []api.EdgeRuleSetVersionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &versions); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("list versions: %d %s", rec.Code, rec.Body.String())
	}
	if len(versions) != 2 || versions[0].Version != 2 || !versions[0].Current || versions[1].RuleCount != 1 {
		t.Fatalf("versions = %+v", versions)
	}

	rec = e.do(t, "GET", "/v1/apps/"+slug+"/edge-rules/versions/1", nil, nil)
	var v1 api.EdgeRuleSetVersionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &v1); err != nil || len(v1.Rules) != 1 || v1.Rules[0].Name != "first" {
		t.Fatalf("get version 1: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, "POST", "/v1/apps/"+slug+"/edge-rules/rollback", api.RollbackEdgeRulesRequest{Version: 1}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback: %d %s", rec.Code, rec.Body.String())
	}
	var restored []api.EdgeRuleResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &restored)
	if len(restored) != 1 || restored[0].ID != created.ID {
		t.Fatalf("restored = %+v, want only the first rule with its original id", restored)
	}
	if got := rec.Header().Get("ETag"); got != `"3"` {
		t.Fatalf("rollback ETag = %q, want a new version \"3\"", got)
	}

	if rec := e.do(t, "POST", "/v1/apps/"+slug+"/edge-rules/rollback", api.RollbackEdgeRulesRequest{Version: 42}, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("rollback to unknown version: %d, want 404", rec.Code)
	}
}
