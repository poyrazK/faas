package meter

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// schedd parks or evicts every app of a suspended, abuse-held or deleting
// account, so it holds no capacity floor for them. Billing that floor would
// charge a reinstated account for capacity it never had (Hunt 16 H16-5).
func TestSampler_MinInstancesFloorSkipsInactiveAccount(t *testing.T) {
	at := func() time.Time { return time.Date(2026, 10, 8, 8, 5, 0, 0, time.UTC) }
	setStatus := func(status state.AccountStatus) func(context.Context, *state.MemStore, string) error {
		return func(ctx context.Context, s *state.MemStore, id string) error {
			return s.UpdateAccountStatus(ctx, id, status)
		}
	}
	for _, tc := range []struct {
		name      string
		hold      func(context.Context, *state.MemStore, string) error
		wantFloor bool
	}{
		{name: "active", wantFloor: true},
		{name: "past_due", hold: setStatus(state.AccountPastDue), wantFloor: true},
		{name: "suspended", hold: setStatus(state.AccountSuspended)},
		{name: "deleted_pending", hold: setStatus(state.AccountDeletedPending)},
		{name: "abuse_held", hold: func(ctx context.Context, s *state.MemStore, id string) error {
			_, err := s.SetAccountAbuseHold(ctx, id, state.AccountAbuseHoldOperator, at())
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			appID, _ := seedFloorApp(t, store, api.PlanScale, 1024)
			setPolicy(t, store, appID, state.ScalingPolicy{MinInstances: 2})
			app, err := store.AppByID(ctx, appID)
			if err != nil {
				t.Fatalf("AppByID: %v", err)
			}
			if tc.hold != nil {
				if err := tc.hold(ctx, store, app.AccountID); err != nil {
					t.Fatalf("hold account: %v", err)
				}
			}
			rows, err := NewSampler(store, nil, at).SampleAndRoll(ctx)
			if err != nil {
				t.Fatalf("SampleAndRoll: %v", err)
			}
			want := 0
			if tc.wantFloor {
				want = 2
			}
			if got := len(syntheticFloorRows(rows, appID)); got != want {
				t.Fatalf("%d synthetic floor rows, want %d", got, want)
			}
		})
	}
}

// A company app's floor follows its organization, like its live rows. An
// owning organization's floor row names the organization and its plan. Either
// kind of organization's suspension stops the floor even though the creator's
// account is active: schedd reads the same resource account.
func TestSampler_CompanyFloorFollowsOrganization(t *testing.T) {
	at := func() time.Time { return time.Date(2026, 10, 8, 8, 5, 0, 0, time.UTC) }
	for _, tc := range []struct {
		name string
		// MemStore turns resource ownership on for a Free organization; a paid
		// one without a billing identity stays a legacy organization.
		plan      api.Plan
		status    state.OrgStatus
		wantFloor bool
	}{
		{name: "owning_active", plan: api.PlanFree, status: state.OrgStatusActive, wantFloor: true},
		{name: "owning_suspended", plan: api.PlanFree, status: state.OrgStatusSuspended},
		{name: "legacy_active", plan: api.PlanScale, status: state.OrgStatusActive, wantFloor: true},
		{name: "legacy_suspended", plan: api.PlanScale, status: state.OrgStatusSuspended},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "member@example.com", api.PlanHobby)
			if err != nil {
				t.Fatalf("CreateAccount: %v", err)
			}
			org, err := store.CreateOrg(ctx, state.Org{Slug: "team", Name: "Team", Plan: tc.plan, Status: tc.status})
			if err != nil {
				t.Fatalf("CreateOrg: %v", err)
			}
			if org.ResourceOwnershipEnabled != (tc.plan == api.PlanFree) {
				t.Fatalf("organization resource ownership %v, want it only for the Free organization", org.ResourceOwnershipEnabled)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, OrgID: org.ID, Slug: "floor", RAMMB: 256, Type: state.AppTypeApp})
			if err != nil {
				t.Fatalf("CreateApp: %v", err)
			}
			if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage}); err != nil {
				t.Fatalf("CreateDeployment: %v", err)
			}
			setPolicy(t, store, app.ID, state.ScalingPolicy{MinInstances: 1})
			rows, err := NewSampler(store, nil, at).SampleAndRoll(ctx)
			if err != nil {
				t.Fatalf("SampleAndRoll: %v", err)
			}
			floor := syntheticFloorRows(rows, app.ID)
			if !tc.wantFloor {
				if len(floor) != 0 {
					t.Fatalf("suspended organization: %d synthetic floor rows, want none", len(floor))
				}
				return
			}
			if len(floor) != 1 {
				t.Fatalf("%d synthetic floor rows, want 1", len(floor))
			}
			wantSubject, wantPlan := acct.ID, api.PlanHobby
			if org.ResourceOwnershipEnabled {
				wantSubject, wantPlan = org.ID, tc.plan
			}
			if floor[0].AccountID != wantSubject || floor[0].Plan != string(wantPlan) {
				t.Fatalf("floor row subject %q plan %q, want %q plan %q",
					floor[0].AccountID, floor[0].Plan, wantSubject, wantPlan)
			}
		})
	}
}

func syntheticFloorRows(rows []RolledRow, appID string) []RolledRow {
	var out []RolledRow
	for _, row := range rows {
		if row.AppID == appID && row.SyntheticFloor {
			out = append(out, row)
		}
	}
	return out
}
