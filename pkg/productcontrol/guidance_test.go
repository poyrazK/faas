// ADR-1548: allowance guidance follows UTC admission counts and current authority.
package productcontrol

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductUsageGuidance(t *testing.T) {
	now := time.Date(2026, 12, 31, 23, 59, 0, 0, time.FixedZone("customer", -5*3600))
	customer := api.ProductCustomer{TenantID: "tenant", Plan: "starter", Revision: "pinned"}
	catalog := api.ProductCatalog{Revision: "pinned", Spec: api.ProductCatalogSpec{Features: map[string]api.ProductFeature{"export": {Label: "Export"}}, Plans: map[string]api.ProductPlan{"starter": {Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}}
	head := api.ProductCatalog{Revision: "latest", Spec: api.ProductCatalogSpec{Plans: map[string]api.ProductPlan{"pro": {}}, SelfService: &api.ProductPlanPolicy{Transitions: map[string][]string{"starter": {"pro"}}}}}
	for _, tc := range []struct {
		name                                       string
		starts, limit                              int64
		run, billing, manage, suspended, available bool
		state, allowance, action                   string
	}{
		{"remaining", 0, 10, true, false, false, false, true, "available", "available", "ask_workspace_admin"},
		{"below threshold", 7, 10, true, false, true, false, true, "available", "available", "review_plans"},
		{"threshold", 8, 10, true, false, true, false, true, "near_limit", "near_limit", "review_plans"},
		{"one left", 9, 10, true, false, false, false, true, "near_limit", "near_limit", "ask_workspace_admin"},
		{"exhausted", 10, 10, true, false, true, false, true, "allowance_exhausted", "exhausted", "review_plans"},
		{"downgraded", 12, 5, true, true, true, false, true, "allowance_exhausted", "exhausted", "review_plans"},
		{"no starts included", 0, 0, true, false, true, false, true, "allowance_exhausted", "no_allowance", "review_plans"},
		{"paused", 8, 10, true, false, true, true, false, "access_paused", "near_limit", "contact_product_team"},
		{"billing cannot run", 9, 10, false, true, false, false, false, "permission_required", "near_limit", "ask_workspace_admin"},
		{"temporarily unavailable", 3, 10, true, false, true, false, false, "temporarily_unavailable", "available", "review_plans"},
		{"unavailable and exhausted", 10, 10, true, false, true, false, false, "temporarily_unavailable", "exhausted", "review_plans"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, spec := customer, catalog
			c.Suspended = tc.suspended
			spec.Spec.Plans["starter"] = api.ProductPlan{Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: tc.limit}}}
			u := api.ProductUsage{Period: Period(now), Lines: []api.ProductUsageLine{{Feature: "export", Revision: "old", Starts: tc.starts / 2}, {Feature: "export", Revision: "new", Starts: tc.starts - tc.starts/2}, {Feature: "another", Starts: 99}}}
			g, err := UsageGuidance("account", "app", c, spec, head, api.ProductPermissions{RunWork: tc.run, ViewBilling: tc.billing, ManagePlan: tc.manage}, &u, map[string]api.ProductResolvedBinding{"export": {Available: tc.available}}, now)
			if err != nil {
				t.Fatal(err)
			}
			row := g.Features[0]
			if row.Starts != tc.starts || row.Remaining != max(0, tc.limit-tc.starts) || row.State != tc.state || row.AllowanceState != tc.allowance || g.PlanAction != tc.action {
				t.Fatalf("guidance: %+v %+v", g, row)
			}
			if g.Period != "2027-01" || !g.ResetsAt.Equal(time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)) || g.ObservedAt.Location() != time.UTC {
				t.Fatalf("UTC month boundary: %+v", g)
			}
			raw, _ := json.Marshal(g)
			for _, field := range []string{"unit_price_millicents", "amount_millicents", "input_schema", "definition_id", "operation_id", "actor_role"} {
				if strings.Contains(string(raw), field) {
					t.Fatalf("private projection leaked %s", field)
				}
			}
		})
	}
}

func TestProductUsageGuidancePrivacyAndReset(t *testing.T) {
	c := api.ProductCustomer{Plan: "starter", Revision: "rev"}
	for _, date := range []struct{ now, reset string }{{"2028-02-29T23:59:59Z", "2028-03-01T00:00:00Z"}, {"2027-02-28T00:00:00Z", "2027-03-01T00:00:00Z"}, {"2026-12-31T23:59:59Z", "2027-01-01T00:00:00Z"}} {
		now, _ := time.Parse(time.RFC3339, date.now)
		g, err := UsageGuidance("a", "b", c, api.ProductCatalog{}, api.ProductCatalog{}, api.ProductPermissions{ViewWork: true}, nil, nil, now)
		if err != nil || g.ResetsAt.Format(time.RFC3339) != date.reset || g.Features == nil || len(g.Features) != 0 || g.RunPermission {
			t.Fatalf("view-only guidance: %+v %v", g, err)
		}
	}
	for _, tc := range []struct {
		starts, limit int64
		want          string
	}{{0, 1, "available"}, {1, 1, "exhausted"}, {3, 5, "available"}, {4, 5, "near_limit"}, {4, 6, "available"}, {5, 6, "near_limit"}, {math.MaxInt64, 10, "exhausted"}} {
		if got := guidanceAllowanceState(tc.starts, tc.limit); got != tc.want {
			t.Fatalf("integer threshold %d/%d: %s", tc.starts, tc.limit, got)
		}
	}
	head := api.ProductCatalog{Revision: c.Revision, Spec: api.ProductCatalogSpec{Plans: map[string]api.ProductPlan{"starter": {}}, SelfService: &api.ProductPlanPolicy{Transitions: map[string][]string{"starter": {"starter", "missing"}}}}}
	if got := guidancePlanAction(c, head, api.ProductPermissions{ManagePlan: true}); got != "contact_product_team" {
		t.Fatalf("unoffered action: %s", got)
	}
}

func TestProductUsageGuidanceInvalidEvidence(t *testing.T) {
	now := time.Now().UTC()
	c := api.ProductCustomer{Plan: "starter", Revision: "rev"}
	catalog := api.ProductCatalog{Revision: "rev", Spec: api.ProductCatalogSpec{Plans: map[string]api.ProductPlan{"starter": {Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10}}}}}}
	for _, lines := range [][]api.ProductUsageLine{{{Feature: "export", Starts: -1}}, {{Feature: "export", Starts: math.MaxInt64}, {Feature: "export", Starts: 1}}} {
		u := api.ProductUsage{Period: Period(now), Lines: lines}
		if _, err := UsageGuidance("a", "b", c, catalog, catalog, api.ProductPermissions{RunWork: true}, &u, nil, now); err == nil {
			t.Fatal("invalid admission counts accepted")
		}
	}
	u := api.ProductUsage{Period: "2000-01"}
	if _, err := UsageGuidance("a", "b", c, catalog, catalog, api.ProductPermissions{RunWork: true}, &u, nil, now); err == nil {
		t.Fatal("historical month presented as current guidance")
	}
}
