package aiconsole

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Company apps share their creator's AccountID: a creator removed from a
// company keeps the integration, but its pages withhold the company app's
// traces and name its spend only as other apps.
func TestPagesWithholdCompanyAppsAfterOffboarding(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner, err := f.store.CreateAccount(ctx, "owner-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := f.store.CreateOrg(ctx, state.Org{Slug: "ai-co", Name: "AI Co", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	for member, role := range map[string]state.OrgRole{owner.ID: state.OrgRoleOwner, f.acct.ID: state.OrgRoleDeveloper} {
		if err := f.store.AddOrgMember(ctx, org.ID, member, role, nil); err != nil {
			t.Fatal(err)
		}
	}
	company, err := f.store.CreateApp(ctx, state.App{AccountID: f.acct.ID, OrgID: org.ID, Slug: "co-api", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	f.store.SeedOutboundAIUsage(f.ai, f.now.Format(dayLayout),
		state.OutboundAIUsageItem{Caller: company.ID, Model: "gpt-5", Requests: 3, CostMillicents: 1_000})
	f.store.SeedOutboundAITrace(f.ai, state.OutboundAITrace{TraceID: "co-1", Caller: company.ID, FirstAt: f.now, LastAt: f.now, Calls: 1})
	pages := func() (traces, day string, trace int) {
		t.Helper()
		return f.get(t, "/dashboard/ai/"+f.ai+"/traces").Body.String(), f.get(t, "/dashboard/ai/"+f.ai).Body.String(),
			f.get(t, "/dashboard/ai/"+f.ai+"/traces/co-1").Code
	}
	traces, day, trace := pages()
	if !strings.Contains(traces, "co-1") || !strings.Contains(day, "co-api") || trace != http.StatusOK {
		t.Fatalf("a member's pages: trace listed %v, app named %v, trace %d",
			strings.Contains(traces, "co-1"), strings.Contains(day, "co-api"), trace)
	}
	if err := f.store.RemoveOrgMember(ctx, org.ID, f.acct.ID); err != nil {
		t.Fatal(err)
	}
	traces, day, trace = pages()
	if strings.Contains(traces, "co-1") || strings.Contains(day, "co-api") || strings.Contains(day, company.ID) ||
		!strings.Contains(day, otherApps) || trace != http.StatusNotFound {
		t.Fatalf("an offboarded creator's pages: trace listed %v, app named %v, other apps %v, trace %d",
			strings.Contains(traces, "co-1"), strings.Contains(day, "co-api"), strings.Contains(day, otherApps), trace)
	}
}
