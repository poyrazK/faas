package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSavedRoutePolicyPlanRevisionAndRecovery(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "pg" {
				store = routePolicyPgStore(t)
			}
			acct, app, inline, intent, saved := savedCheckFixture(t, store)
			tx := store.(state.RoutePolicyStore)
			request := api.RoutePolicyPlanRequest{Saved: true, DeploymentID: inline.DeploymentID, ThrottleBurst: inline.ThrottleBurst}
			planner := routeGroupTestPlanner(request)
			plan, err := tx.PlanRoutePolicy(t.Context(), acct.ID, app.ID, request, planner)
			if err != nil || plan.Status != "ready" || plan.RequirementsRevision != saved.Revision || plan.RequirementsSHA256 != saved.SHA256 || plan.Requirements == nil {
				t.Fatalf("saved plan=%+v err=%v", plan, err)
			}
			// Selecting the same intent from a local file has a distinct fingerprint.
			local, err := tx.PlanRoutePolicy(t.Context(), acct.ID, app.ID, inline.RoutePolicyPlanRequest, routeGroupTestPlanner(inline.RoutePolicyPlanRequest))
			if err != nil || local.SHA256 == plan.SHA256 || local.RequirementsRevision != 0 {
				t.Fatalf("source binding: %+v %v", local, err)
			}
			apply := api.RoutePolicyApplyRequest{RoutePolicyPlanRequest: request, Confirm: true, ExpectedPlanSHA256: plan.SHA256}
			if _, _, err := tx.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "unpinned", apply, planner); err == nil {
				t.Fatal("saved apply accepted missing revision")
			}
			apply.ExpectedRevision = &plan.RequirementsRevision
			config, _, _ := api.CanonicalRouteRequirements(saved.Requirements)
			max := int64(700)
			config.Groups[0].Require.Budget.MaxMS = &max
			changed, err := intent.SaveRouteRequirements(t.Context(), acct.ID, app.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &saved.Revision, Requirements: config})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := tx.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "stale", apply, planner); !errors.Is(err, state.ErrRouteRequirementsRevision) {
				t.Fatalf("changed intent accepted: %v", err)
			}
			if _, err := tx.PlanRoutePolicy(t.Context(), acct.ID, app.ID, apply.RoutePolicyPlanRequest, planner); !errors.Is(err, state.ErrRouteRequirementsRevision) {
				t.Fatalf("pinned stale planning: %v", err)
			}
			// Restoring identical content still changes revision and invalidates review.
			restored, err := intent.SaveRouteRequirements(t.Context(), acct.ID, app.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &changed.Revision, Requirements: saved.Requirements})
			if err != nil || restored.SHA256 != saved.SHA256 || restored.Revision != 3 {
				t.Fatalf("restore: %+v %v", restored, err)
			}
			if _, _, err := tx.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "restored-stale", apply, planner); !errors.Is(err, state.ErrRouteRequirementsRevision) {
				t.Fatalf("restored intent reused old review: %v", err)
			}
			apply.ExpectedRevision = &restored.Revision
			if _, _, err := tx.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "changed-pin", apply, planner); !errors.Is(err, state.ErrRoutePolicyStale) {
				t.Fatalf("changed revision pin bypassed reviewed fingerprint: %v", err)
			}
			converted := inline
			converted.ExpectedPlanSHA256 = plan.SHA256
			if _, _, err := tx.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "changed-source", converted, routeGroupTestPlanner(converted.RoutePolicyPlanRequest)); !errors.Is(err, state.ErrRoutePolicyStale) {
				t.Fatalf("inline source bypassed saved fingerprint: %v", err)
			}
			rules, _ := store.ListEdgeRulesForApp(t.Context(), app.ID)
			if len(rules) != 0 {
				t.Fatal("stale repairs wrote rules")
			}
			if _, err := tx.FindRoutePolicyReceipt(t.Context(), acct.ID, app.ID, "stale", apply); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("stale repair wrote receipt")
			}
			fresh, err := tx.PlanRoutePolicy(t.Context(), acct.ID, app.ID, request, planner)
			if err != nil || fresh.SHA256 == plan.SHA256 || fresh.RequirementsRevision != restored.Revision {
				t.Fatalf("fresh plan: %+v %v", fresh, err)
			}
			apply.ExpectedRevision, apply.ExpectedPlanSHA256 = &fresh.RequirementsRevision, fresh.SHA256
			receipt, replay, err := tx.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "repair", apply, planner)
			if err != nil || replay || receipt.Verification.Status != "satisfied" {
				t.Fatalf("repair: %+v %t %v", receipt, replay, err)
			}
			if _, err := intent.SaveRouteRequirements(t.Context(), acct.ID, app.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &restored.Revision, Requirements: config}); err != nil {
				t.Fatal(err)
			}
			if err := store.DeleteDeploymentOpenAPIDoc(t.Context(), request.DeploymentID, acct.ID); err != nil {
				t.Fatal(err)
			}
			second, replay, err := tx.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "repair", apply, planner)
			if err != nil || !replay || second.ID != receipt.ID {
				t.Fatalf("receipt recovery: %+v %t %v", second, replay, err)
			}
			apply.ExpectedRevision = &saved.Revision
			if _, _, err := tx.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "repair", apply, planner); !errors.Is(err, state.ErrRoutePolicyKeyReused) {
				t.Fatalf("changed retry request: %v", err)
			}
		})
	}
}

func TestSavedRoutePolicyApplyFencesIntentWriter(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "pg" {
				store = routePolicyPgStore(t)
			}
			acct, app, inline, intent, saved := savedCheckFixture(t, store)
			request := api.RoutePolicyPlanRequest{Saved: true, DeploymentID: inline.DeploymentID, ThrottleBurst: inline.ThrottleBurst, ExpectedRevision: &saved.Revision}
			original := routeGroupTestPlanner(request)
			tx := store.(state.RoutePolicyStore)
			plan, err := tx.PlanRoutePolicy(t.Context(), acct.ID, app.ID, request, original)
			if err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			calls := 0
			planner := func(snapshot state.RoutePolicySnapshot) (api.RoutePolicyPlan, error) {
				calls++
				if calls == 1 {
					close(entered)
					<-release
				}
				return original(snapshot)
			}
			applied := make(chan error, 1)
			go func() {
				_, _, err := tx.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "fenced-repair", api.RoutePolicyApplyRequest{RoutePolicyPlanRequest: request, Confirm: true, ExpectedPlanSHA256: plan.SHA256}, planner)
				applied <- err
			}()
			<-entered
			config, _, _ := api.CanonicalRouteRequirements(saved.Requirements)
			value := int64(700)
			config.Groups[0].Require.Budget.MaxMS = &value
			written := make(chan error, 1)
			go func() {
				_, err := intent.SaveRouteRequirements(t.Context(), acct.ID, app.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &saved.Revision, Requirements: config})
				written <- err
			}()
			var early bool
			select {
			case <-written:
				early = true
			case <-time.After(50 * time.Millisecond):
			}
			close(release)
			if err := <-applied; err != nil {
				t.Fatalf("repair: %v", err)
			}
			if early {
				t.Fatal("saved intent writer escaped apply lock")
			}
			if err := <-written; err != nil {
				t.Fatal(err)
			}
		})
	}
}
