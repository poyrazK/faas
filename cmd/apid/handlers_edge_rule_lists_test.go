package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-963 over HTTP: create, reference from a rule, edit, and the delete
// guard.
func TestEdgeRuleListLifecycle(t *testing.T) {
	e := setup(t, api.PlanHobby)
	slug := mustSeedEdgeRuleApp(t, e, "lists")

	rec := e.do(t, "POST", "/v1/edge-rule-lists", api.CreateEdgeRuleListRequest{
		Name: "office", Kind: "ip", Items: []string{"203.0.113.7/24", "2001:DB8::1"},
	}, nil)
	var created api.EdgeRuleListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if rec.Code != http.StatusCreated || !reflect.DeepEqual(created.Items, []string{"2001:db8::1", "203.0.113.0/24"}) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "POST", "/v1/edge-rule-lists", api.CreateEdgeRuleListRequest{Name: "office", Kind: "ip"}, nil); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate name: %d, want 409", rec.Code)
	}
	if rec := e.do(t, "POST", "/v1/edge-rule-lists", api.CreateEdgeRuleListRequest{Name: "bad", Kind: "ip", Items: []string{"nope"}}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid item: %d, want 400", rec.Code)
	}

	ruleWith := func(expr api.EdgeRuleMatchExpr) *httptest.ResponseRecorder {
		req := edgeRuleRouteReq("canary")
		req.MatchHost = "api.example.com"
		req.Match = &expr
		return e.do(t, "POST", "/v1/apps/"+slug+"/edge-rules", req, nil)
	}
	if rec := ruleWith(api.EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "missing"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown list: %d, want 400", rec.Code)
	}
	if rec := ruleWith(api.EdgeRuleMatchExpr{Field: "country", Op: "in_list", List: "office"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("kind mismatch: %d, want 400", rec.Code)
	}
	rec = ruleWith(api.EdgeRuleMatchExpr{Not: &api.EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "office"}})
	var rule api.EdgeRuleResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &rule)
	if rec.Code != http.StatusCreated {
		t.Fatalf("rule with list: %d %s", rec.Code, rec.Body.String())
	}

	rec = e.do(t, "GET", "/v1/edge-rule-lists", nil, nil)
	var index api.ListEdgeRuleListsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &index)
	if len(index.Lists) != 1 || index.Lists[0].ItemCount != 2 || index.Lists[0].Items != nil ||
		!reflect.DeepEqual(index.Lists[0].ReferencedBy, []string{rule.ID}) {
		t.Fatalf("index = %s", rec.Body.String())
	}

	before, _ := e.store.GetEdgeRuleByID(context.Background(), rule.ID)
	time.Sleep(2 * time.Millisecond)
	rec = e.do(t, "PATCH", "/v1/edge-rule-lists/office", api.UpdateEdgeRuleListRequest{
		Add: []string{"198.51.100.1"}, Remove: []string{"2001:db8:0::1"},
	}, nil)
	var updated api.EdgeRuleListResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if rec.Code != http.StatusOK || !reflect.DeepEqual(updated.Items, []string{"198.51.100.1", "203.0.113.0/24"}) {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	after, _ := e.store.GetEdgeRuleByID(context.Background(), rule.ID)
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatal("list edit must touch the referencing rule so gateways recompile it")
	}
	replace := []string{"10.0.0.1"}
	if rec := e.do(t, "PATCH", "/v1/edge-rule-lists/office", api.UpdateEdgeRuleListRequest{Items: &replace, Add: []string{"10.0.0.2"}}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("items with add: %d, want 400", rec.Code)
	}

	if rec := e.do(t, "DELETE", "/v1/edge-rule-lists/office", nil, nil); rec.Code != http.StatusConflict {
		t.Fatalf("delete in use: %d, want 409", rec.Code)
	}
	if rec := e.do(t, "DELETE", "/v1/edge-rules/"+rule.ID, nil, nil); rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("delete rule: %d", rec.Code)
	}
	if rec := e.do(t, "DELETE", "/v1/edge-rule-lists/office", nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "GET", "/v1/edge-rule-lists/office", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted: %d, want 404", rec.Code)
	}
}

func TestEdgeRuleListPlanLimits(t *testing.T) {
	free := setup(t, api.PlanFree)
	if rec := free.do(t, "POST", "/v1/edge-rule-lists", api.CreateEdgeRuleListRequest{Name: "x", Kind: "country"}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("free plan create: %d, want 403", rec.Code)
	}

	e := setup(t, api.PlanHobby)
	limits := api.MustLimitsFor(api.PlanHobby)
	for i := 0; i < limits.EdgeRuleListsPerAccount; i++ {
		if rec := e.do(t, "POST", "/v1/edge-rule-lists", api.CreateEdgeRuleListRequest{Name: fmt.Sprintf("l%d", i), Kind: "country"}, nil); rec.Code != http.StatusCreated {
			t.Fatalf("list %d: %d", i, rec.Code)
		}
	}
	if rec := e.do(t, "POST", "/v1/edge-rule-lists", api.CreateEdgeRuleListRequest{Name: "over", Kind: "country"}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("over list cap: %d, want 403", rec.Code)
	}
	items := make([]string, limits.EdgeRuleListMaxItems+1)
	for i := range items {
		items[i] = fmt.Sprintf("10.0.%d.%d", i/256, i%256)
	}
	if rec := e.do(t, "PATCH", "/v1/edge-rule-lists/l0", api.UpdateEdgeRuleListRequest{Items: &items}, nil); rec.Code != http.StatusBadRequest {
		// l0 is a country list, so IPs are rejected before the size cap.
		t.Fatalf("wrong-kind items: %d, want 400", rec.Code)
	}
	if rec := e.do(t, "POST", "/v1/edge-rule-lists", api.CreateEdgeRuleListRequest{Name: "big", Kind: "ip", Items: items}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("over item cap: %d, want 403", rec.Code)
	}
}
