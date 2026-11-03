package apidserver_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-11-03 (review round 9): lists and CORS presets
// are account objects, and a company app's rules resolve them in the
// namespace of the account that owns its resources, so no admin but the
// app's creator could manage them, and once the creator left the lists its
// rules used were frozen. With ?app=<company app> an organization's members
// manage that organization's lists and the app's presets with their role on
// the app, and never see the creator's personal ones.
func TestCompanyAppEdgeRuleListsAndPresetsFollowOrganizationRoles(t *testing.T) {
	useTestSealRecipient(t)
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	creator := *e.Acct()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "list-team", "List Team", api.PlanPro)
	if r := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "list-team-api"}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create company app = %d %s", r.Code, r.Body)
	}
	admin, asAdmin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, asViewer := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	const company = "?app=list-team-api"
	names := func(headers map[string]string, query string) []string {
		t.Helper()
		r := e.Do(t, http.MethodGet, "/v1/edge-rule-lists"+query, nil, headers)
		if r.Code != http.StatusOK {
			t.Fatalf("list lists%s = %d %s", query, r.Code, r.Body)
		}
		var out api.ListEdgeRuleListsResponse
		if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, l := range out.Lists {
			got = append(got, l.Name)
		}
		return got
	}

	if r := e.Do(t, http.MethodPost, "/v1/edge-rule-lists", api.CreateEdgeRuleListRequest{Name: "creator-private", Kind: "ip", Items: []string{"198.51.100.0/24"}}, nil); r.Code != http.StatusCreated {
		t.Fatalf("creator's personal list = %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodPost, "/v1/edge-rule-lists"+company, api.CreateEdgeRuleListRequest{Name: "team-ips", Kind: "ip", Items: []string{"203.0.113.0/24"}}, asViewer); r.Code != http.StatusForbidden {
		t.Fatalf("viewer creating a company list = %d %s, want 403", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodPost, "/v1/edge-rule-lists"+company, api.CreateEdgeRuleListRequest{Name: "team-ips", Kind: "ip", Items: []string{"203.0.113.0/24"}}, asAdmin); r.Code != http.StatusCreated {
		t.Fatalf("admin creating a company list = %d %s, want 201", r.Code, r.Body)
	}
	if got := names(asViewer, company); !slices.Equal(got, []string{"team-ips"}) {
		t.Fatalf("company lists = %v, want [team-ips] without the creator's personal list", got)
	}
	if got := names(nil, ""); !slices.Equal(got, []string{"creator-private"}) {
		t.Fatalf("creator's personal lists = %v, want [creator-private]", got)
	}
	if r := e.Do(t, http.MethodGet, "/v1/edge-rule-lists/creator-private"+company, nil, asAdmin); r.Code != http.StatusNotFound {
		t.Fatalf("admin reading the creator's personal list = %d %s, want 404", r.Code, r.Body)
	}
	// The company app's rules use the organization's list.
	rule := api.CreateEdgeRuleRequest{MatchHost: api.EdgeRuleAppHosts, MatchPath: "/**", Kind: string(state.EdgeRuleKindIP),
		Match:  &api.EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: "team-ips"},
		Action: json.RawMessage(`{"deny":["192.0.2.0/24"]}`)}
	if r := e.Do(t, http.MethodPost, "/v1/apps/list-team-api/edge-rules", rule, asAdmin); r.Code != http.StatusCreated {
		t.Fatalf("admin rule using the company list = %d %s, want 201", r.Code, r.Body)
	}

	// The creator leaves; the company still manages its list.
	if err := store.TransferOrgOwnership(t.Context(), org.ID, creator.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, http.MethodDelete, "/v1/orgs/"+org.Slug+"/members/"+creator.ID, nil, asAdmin); r.Code >= 300 {
		t.Fatalf("remove creator = %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodPatch, "/v1/edge-rule-lists/team-ips"+company, api.UpdateEdgeRuleListRequest{Add: []string{"192.0.2.0/24"}}, asAdmin); r.Code != http.StatusOK {
		t.Fatalf("admin editing the company list after the creator left = %d %s, want 200", r.Code, r.Body)
	}

	// CORS presets: the company app's own, never the creator's account-wide ones.
	if r := e.Do(t, http.MethodPost, "/v1/cors-presets"+company, api.CreateCorsPresetRequest{Name: "team", AllowOrigins: []string{"https://team.example.com"}, AllowMethods: []string{"GET"}}, asAdmin); r.Code != http.StatusCreated {
		t.Fatalf("admin creating a company preset = %d %s, want 201", r.Code, r.Body)
	}
	r := e.Do(t, http.MethodGet, "/v1/cors-presets"+company, nil, asViewer)
	var presets api.CorsPresetListResponse
	if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &presets) != nil || len(presets.Presets) != 1 || presets.Presets[0].Name != "team" || presets.Presets[0].AppID == nil {
		t.Fatalf("company presets = %d %s, want the one app-scoped preset", r.Code, r.Body)
	}
}
