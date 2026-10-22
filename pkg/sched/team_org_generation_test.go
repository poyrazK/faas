// adr: 1740, 1741 — a team organization's status, plan and abuse hold live on
// its row, which moves no runtime generation, so neither an owner cache entry
// nor a thaw ticket may stand in for them.
package sched_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
)

// seedTeamOrgApp creates a live function app owned by a team organization.
func seedTeamOrgApp(t *testing.T, mem *state.MemStore) (state.App, state.Org) {
	t.Helper()
	ctx := t.Context()
	acct, err := mem.CreateAccount(ctx, "member-"+uuid.NewString()[:8]+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := mem.CreateOrg(ctx, state.Org{Slug: "team-" + uuid.NewString()[:8], Name: "Team", Plan: api.PlanPro,
		Status: state.OrgStatusActive, ResourceOwnershipEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, OrgID: org.ID, Slug: "team-app-" + uuid.NewString()[:8],
		Type: state.AppTypeFunction, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:team", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	return app, org
}

// The ordinary wake path admits under state.AppResourceAccount, which takes a
// team organization's status from its row. An owner cache entry read the
// member's account instead and kept serving after the organization was
// suspended.
func TestOwnerCacheRefusesNothingTheOrganizationRefuses(t *testing.T) {
	mem := state.NewMemStore()
	app, org := seedTeamOrgApp(t, mem)
	ops := wire.NewOpsMetrics("schedd")
	e := sched.XNewEngine(t, statetest.NewMem(&sched.ReadCountingStore{MemStore: mem}), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").
		WithOpsMetrics(ops).WithOwnerCache()
	result, err := e.Wake(t.Context(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if err := e.Park(t.Context(), result.InstanceID); err != nil {
		t.Fatal(err)
	}
	// An organization change moves no runtime generation.
	if err := mem.UpdateOrgStatus(t.Context(), org.ID, state.OrgStatusSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Wake(t.Context(), app.ID, "", "", ""); !errors.Is(err, sched.ErrPermanentWake) {
		t.Fatalf("Wake of a suspended organization's app = %v, want ErrPermanentWake", err)
	}
	if hits := testutil.ToFloat64(ops.OwnerCacheTotal("hit")); hits != 0 {
		t.Fatalf("owner cache hits = %v for a team organization's app, want none", hits)
	}
}

// A thaw ticket read the member's account too, so the fast path thawed a
// suspended organization's VM.
func TestThawTicketRefusesATeamOrganizationApp(t *testing.T) {
	mem := state.NewMemStore()
	org, err := mem.CreateOrg(t.Context(), state.Org{Slug: "team-" + uuid.NewString()[:8], Name: "Team", Plan: api.PlanPro,
		Status: state.OrgStatusActive, ResourceOwnershipEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	f := sched.NewFrozenFixtureInStore(t, mem, true, func(app *state.App) { app.OrgID = org.ID })
	if (*f.App()).OrgID != org.ID {
		t.Fatalf("fixture app org = %q, want %q", (*f.App()).OrgID, org.ID)
	}
	ops := wire.NewOpsMetrics("schedd")
	(*f.E()).WithOpsMetrics(ops)
	(*f.E()).EnableFrozenTier()
	(*f.E()).EnableFrozenFastPath()
	if handled, err := (*f.E()).FreezeIdle(t.Context(), (*f.Ins()).ID, 150); err != nil || !handled {
		t.Fatalf("FreezeIdle: %v, %v", handled, err)
	}
	if n := testutil.ToFloat64(ops.FrozenThawTickets("organization")); n != 1 {
		t.Fatalf("tickets refused for the organization = %v, want 1 (issued %v)", n, testutil.ToFloat64(ops.FrozenThawTickets("issued")))
	}
	if err := mem.UpdateOrgStatus(t.Context(), org.ID, state.OrgStatusSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := (*f.E()).Wake(t.Context(), (*f.App()).ID, "", "", ""); !errors.Is(err, sched.ErrPermanentWake) {
		t.Fatalf("Wake of a suspended organization's frozen app = %v, want ErrPermanentWake", err)
	}
}
