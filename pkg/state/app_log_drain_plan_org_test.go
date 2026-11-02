package state_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// appLogDrainOrgPlanCase pins that delivery checks a company app's drains
// against the plan of the organization once it adopted billing, not the
// plan of the member who created the app, and spends the organization's
// account-wide allowance rather than the creator's (logs audit 2026-10-31
// D3). adopt makes the organization the app's billing subject.
func appLogDrainOrgPlanCase(t *testing.T, store state.Store, adopt func(orgID string)) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	creator, err := store.CreateAccount(ctx, "drain-plan-creator-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "drain-plan-" + suffix, Name: "Drain plan", Plan: api.PlanFree,
		Status: state.OrgStatusActive, ResourceOwnershipEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	adopt(org.ID)
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	company, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "drain-plan-company-" + suffix, Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, Slug: "drain-plan-personal-" + suffix, Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	drain := func(app state.App) string {
		t.Helper()
		d, err := store.CreateAppLogDrain(ctx, state.AppLogDrain{AppID: app.ID, AccountID: creator.ID, Kind: state.AppLogDrainKindHTTPJSON,
			TargetURL: "https://logs.example/" + uuid.NewString(), Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	companyDrain, personalDrain := drain(company), drain(personal)
	expect := func(step string, wantCompany bool) {
		t.Helper()
		listed, err := store.ListEnabledAppLogDrains(ctx)
		if err != nil {
			t.Fatal(err)
		}
		delivered := map[string]bool{}
		for _, d := range listed {
			delivered[d.ID] = true
		}
		if delivered[companyDrain] != wantCompany || !delivered[personalDrain] {
			t.Fatalf("%s: company drain delivered = %v (want %v), personal drain delivered = %v (want true)",
				step, delivered[companyDrain], wantCompany, delivered[personalDrain])
		}
		paused, err := store.ListAppLogDrainsPausedByPlan(ctx, company.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{companyDrain}
		if wantCompany {
			want = nil
		}
		if !slices.Equal(paused, want) {
			t.Fatalf("%s: company app's paused drains = %v, want %v", step, paused, want)
		}
	}
	expect("free organization, pro creator", false)
	if err := store.UpdateOrgPlan(ctx, org.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	expect("hobby organization", true)
}

func TestMemStoreAppLogDrainsFollowTheOrganizationPlan(t *testing.T) {
	appLogDrainOrgPlanCase(t, state.NewMemStore(), func(string) {})
}

func TestPgAppLogDrainsFollowTheOrganizationPlan(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	appLogDrainOrgPlanCase(t, store, func(orgID string) {
		if _, err := pool.Exec(ctx, `update orgs set resource_ownership_enabled = true where id = $1`, orgID); err != nil {
			t.Fatal(err)
		}
	})
}
