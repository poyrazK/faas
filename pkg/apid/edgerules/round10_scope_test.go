package edgerules

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type round10Fixture struct {
	svc            *Service
	mem            *state.MemStore
	acct           state.Account
	personal, team state.App
	org            state.Org
	accountWide    state.CorsPreset
	teamPreset     state.CorsPreset
	personalPreset state.CorsPreset
	personalList   state.EdgeRuleList
	orgList        state.EdgeRuleList
}

func newRound10Fixture(t *testing.T) round10Fixture {
	t.Helper()
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "round10@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := mem.CreateOrg(ctx, state.Org{Slug: "round10-team", Name: "Team", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(slug, orgID string) state.App {
		app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, OrgID: orgID, Slug: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	f := round10Fixture{mem: mem, acct: acct, org: org, personal: mk("round10-mine", ""), team: mk("round10-team-api", org.ID)}
	limits := api.MustLimitsFor(api.PlanPro)
	preset := func(name, appID string) state.CorsPreset {
		p, err := mem.CreateCorsPresetIfUnderQuota(ctx, state.CorsPreset{AccountID: acct.ID, AppID: appID, Name: name,
			AllowOrigins: []string{"https://a.example.com"}, AllowMethods: []string{"GET"}, MaxAgeSeconds: 600}, limits)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	f.accountWide, f.teamPreset, f.personalPreset = preset("wide", ""), preset("team", f.team.ID), preset("mine", f.personal.ID)
	list := func(name, orgID string) state.EdgeRuleList {
		l, err := mem.CreateEdgeRuleList(ctx, state.CreateEdgeRuleListParams{AccountID: acct.ID, OrgID: orgID, Name: name, Kind: "ip", Items: []string{"203.0.113.0/24"}}, 100)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	f.personalList, f.orgList = list("private-ips", ""), list("team-ips", org.ID)
	f.svc = &Service{Host: attackModeHost{store: mem, audits: &[]string{}}}
	return f
}

// adr: 091 — a cors rule's preset reference is kept, and a rule names only a
// preset its app may use (amendment 2026-11-04, review round 10).
func TestActionFromBodyKeepsTheCorsPreset(t *testing.T) {
	action := ActionFromBody("cors", json.RawMessage(`{"cors_preset_id":"p-1"}`))
	if action.CORS == nil || action.CORS.CorsPresetID == nil || *action.CORS.CorsPresetID != "p-1" {
		t.Fatalf("cors action = %+v, want the preset reference kept", action.CORS)
	}
	params := edgeRuleUpdateParamsFrom(api.UpdateEdgeRuleRequest{Action: ptrRaw(`{"allow_origins":["https://a.example.com"]}`)}, state.EdgeRuleKindCORSA)
	if params.CorsPresetID == nil || *params.CorsPresetID != nil {
		t.Fatalf("inline cors update preset column = %v, want an explicit clear", params.CorsPresetID)
	}
}

func ptrRaw(s string) *json.RawMessage {
	raw := json.RawMessage(s)
	return &raw
}

func TestCorsPresetRefProblemKeepsPresetsInTheirScope(t *testing.T) {
	f := newRound10Fixture(t)
	missing := "00000000-0000-0000-0000-000000000009"
	for _, tc := range []struct {
		name    string
		app     state.App
		preset  string
		refused bool
	}{
		{"personal app, account-wide preset", f.personal, f.accountWide.ID, false},
		{"personal app, its own preset", f.personal, f.personalPreset.ID, false},
		{"personal app, a company app's preset", f.personal, f.teamPreset.ID, true},
		{"company app, its own preset", f.team, f.teamPreset.ID, false},
		{"company app, the creator's account-wide preset", f.team, f.accountWide.ID, true},
		{"company app, the creator's personal app preset", f.team, f.personalPreset.ID, true},
		{"missing preset", f.personal, missing, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := tc.preset
			prob := f.svc.CorsPresetRefProblem(context.Background(), f.acct.ID, tc.app, &id)
			if (prob != nil) != tc.refused {
				t.Fatalf("problem = %v, want refused %v", prob, tc.refused)
			}
		})
	}
}

func TestRuleListsStayInTheirAppsScope(t *testing.T) {
	f := newRound10Fixture(t)
	match := func(list string) *api.EdgeRuleMatchExpr {
		return &api.EdgeRuleMatchExpr{Field: "client_ip", Op: "in_list", List: list}
	}
	for _, tc := range []struct {
		name    string
		app     state.App
		list    string
		refused bool
	}{
		{"personal rule, personal list", f.personal, "private-ips", false},
		{"personal rule, organization list", f.personal, "team-ips", true},
		{"company rule, organization list", f.team, "team-ips", false},
		{"company rule, creator's unshared personal list", f.team, "private-ips", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prob := f.svc.validateEdgeRuleMatchLists(context.Background(), f.acct.ID, tc.app, match(tc.list))
			if (prob != nil) != tc.refused {
				t.Fatalf("problem = %v, want refused %v", prob, tc.refused)
			}
		})
	}
}

// The list cap counts each scope's lists separately.
func TestEdgeRuleListCapCountsItsScope(t *testing.T) {
	f := newRound10Fixture(t)
	ctx := context.Background()
	if _, err := f.mem.CreateEdgeRuleList(ctx, state.CreateEdgeRuleListParams{AccountID: f.acct.ID, OrgID: f.org.ID, Name: "team-2", Kind: "ip"}, 2); err != nil {
		t.Fatalf("second organization list under a cap of 2: %v", err)
	}
	if _, err := f.mem.CreateEdgeRuleList(ctx, state.CreateEdgeRuleListParams{AccountID: f.acct.ID, Name: "mine-2", Kind: "ip"}, 2); err != nil {
		t.Fatalf("second personal list under a cap of 2: %v", err)
	}
	if _, err := f.mem.CreateEdgeRuleList(ctx, state.CreateEdgeRuleListParams{AccountID: f.acct.ID, Name: "mine-3", Kind: "ip"}, 2); err == nil {
		t.Fatal("third personal list passed a cap of 2")
	}
}
