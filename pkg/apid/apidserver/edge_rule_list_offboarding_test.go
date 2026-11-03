package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-30: a company app's rules stay under its
// creator's account, and so do the lists they use. Editing a list changes
// what those rules let through, so it gets the organization check an app
// route gets: a creator removed from the company can no longer change the
// company app's IP gate by editing the list behind it.
func TestEdgeRuleListEditAuthorizesTheAppsUsingIt(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, creator := *e.Store(), *e.Acct()
	ctx := t.Context()
	owner, err := store.CreateAccount(ctx, "acme-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "acme-api",
		Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	do := func(method, path string, body any, key string) (int, string) {
		t.Helper()
		rec := e.Do(t, method, path, body, map[string]string{"Idempotency-Key": key})
		return rec.Code, rec.Body.String()
	}
	if code, body := do(http.MethodPost, "/v1/edge-rule-lists", api.CreateEdgeRuleListRequest{
		Name: "blocked", Kind: "ip", Items: []string{"203.0.113.0/24"},
	}, "list"); code != http.StatusCreated {
		t.Fatalf("create list: %d %s", code, body)
	}
	if code, body := do(http.MethodPost, "/v1/apps/acme-api/edge-rules", map[string]any{
		"kind": "ip", "match_host": api.EdgeRuleAppHosts, "match_path": "/**",
		"action": map[string]any{"deny": []string{"0.0.0.0/0", "::/0"}},
		"match":  map[string]any{"field": "client_ip", "op": "in_list", "list": "blocked"},
	}, "rule"); code != http.StatusCreated {
		t.Fatalf("create the company app's rule: %d %s", code, body)
	}
	edit := func(item, key string) (int, string) {
		items := []string{item}
		return do(http.MethodPatch, "/v1/edge-rule-lists/blocked", api.UpdateEdgeRuleListRequest{Items: &items}, key)
	}
	if code, body := edit("198.51.100.0/24", "member-edit"); code != http.StatusOK {
		t.Fatalf("a company admin's list edit = %d %s, want 200", code, body)
	}

	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	if code, body := edit("192.0.2.0/24", "offboarded-edit"); code < 400 || code >= 500 {
		t.Fatalf("an offboarded creator's list edit = %d %s, want a 4xx refusal", code, body)
	}
	lists, err := store.EdgeRuleListsByName(ctx, creator.ID, []string{"blocked"})
	if err != nil || len(lists) != 1 || len(lists[0].Items) != 1 || lists[0].Items[0] != "198.51.100.0/24" {
		t.Fatalf("list after the refused edit = %+v, %v; want the member's edit kept", lists, err)
	}
}
