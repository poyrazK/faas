package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-832 over HTTP: a valid condition round-trips, an invalid one is
// rejected before anything is stored, and PATCH can replace or clear it.
func TestEdgeRuleMatchConditionCreateUpdateClear(t *testing.T) {
	e := setup(t, api.PlanHobby)
	slug := mustSeedEdgeRuleApp(t, e, "conditions")

	var cond api.EdgeRuleMatchExpr
	if err := json.Unmarshal([]byte(`{"all":[{"field":"cookie:beta","op":"eq","value":"1"},{"not":{"field":"client_ip","op":"cidr","values":["10.0.0.0/8"]}}]}`), &cond); err != nil {
		t.Fatal(err)
	}
	req := edgeRuleRouteReq("canary")
	req.MatchHost = "api.example.com"
	req.Match = &cond
	rec := e.do(t, "POST", "/v1/apps/"+slug+"/edge-rules", req, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create with condition: %d %s", rec.Code, rec.Body.String())
	}
	var created api.EdgeRuleResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Match == nil || len(created.Match.All) != 2 || created.Match.All[1].Not == nil {
		t.Fatalf("condition not round-tripped: %+v", created.Match)
	}

	bad := edgeRuleRouteReq("canary")
	bad.MatchHost = "api.example.com"
	bad.Match = &api.EdgeRuleMatchExpr{Field: "client_ip", Op: "prefix", Value: "10."}
	if rec := e.do(t, "POST", "/v1/apps/"+slug+"/edge-rules", bad, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid condition: %d %s, want 400", rec.Code, rec.Body.String())
	}

	replacement := api.EdgeRuleMatchExpr{Field: "header:X-Tier", Op: "in", Values: []string{"gold"}}
	rec = e.do(t, "PATCH", "/v1/edge-rules/"+created.ID, api.UpdateEdgeRuleRequest{Match: &replacement}, nil)
	var updated api.EdgeRuleResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if rec.Code != http.StatusOK || updated.Match == nil || updated.Match.Field != "header:X-Tier" {
		t.Fatalf("replace condition: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, "PATCH", "/v1/edge-rules/"+created.ID, api.UpdateEdgeRuleRequest{Match: &replacement, ClearMatch: true}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("match + clear_match: %d, want 400", rec.Code)
	}
	rec = e.do(t, "PATCH", "/v1/edge-rules/"+created.ID, api.UpdateEdgeRuleRequest{ClearMatch: true}, nil)
	var cleared api.EdgeRuleResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &cleared)
	if rec.Code != http.StatusOK || cleared.Match != nil {
		t.Fatalf("clear condition: %d %s", rec.Code, rec.Body.String())
	}
}
