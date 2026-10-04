package state_test

// ADR-448: revisioned current route intent, tenant isolation and snapshot checks.

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/routerequirements"
	"github.com/onebox-faas/faas/pkg/state"
)

func savedCheckFixture(t *testing.T, store state.Store) (state.Account, state.App, api.RoutePolicyApplyRequest, state.RouteRequirementsStore, api.SavedRouteRequirements) {
	t.Helper()
	acct, app, policy, _ := routeGroupFixture(t, store)
	policy.Requirements.Groups[0].Require.Throttle = nil
	zero := int64(0)
	apiStore := store.(state.RouteRequirementsStore)
	saved, err := apiStore.SaveRouteRequirements(t.Context(), acct.ID, app.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &zero, Requirements: policy.Requirements})
	if err != nil || saved.Revision != 1 {
		t.Fatalf("first save: %+v %v", saved, err)
	}
	return acct, app, policy, apiStore, saved
}

func testSavedRequirementsChecker(deployment string) state.RouteRequirementsChecker {
	return func(snapshot state.RoutePolicySnapshot, saved api.SavedRouteRequirements) (api.RouteRequirementsCheck, error) {
		inventory := routerequirements.CoverageInventory{Status: "unavailable", Source: "captured_candidate_contract"}
		if contract := snapshot.Contract; contract != nil && !contract.Truncated && len(contract.Doc) > 0 {
			spec, err := openapidiff.LoadBytes(contract.Doc)
			if err != nil {
				return api.RouteRequirementsCheck{}, err
			}
			inventory = routerequirements.CandidateInventory(spec, contract.DeploymentID, contract.SHA256)
		}
		context := routerequirements.Context{Host: snapshot.App.Slug + ".gregale.dev", Rules: snapshot.Rules,
			App: api.AppResponse{ID: snapshot.App.ID, Slug: snapshot.App.Slug, ConsumerAuthMode: string(snapshot.App.ConsumerAuthMode), EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 10000, RequestBudgetMaxMS: 20000}}}
		return routerequirements.BuildSavedCheck(saved, context, string(snapshot.Account.Plan), deployment, inventory)
	}
}

func TestSavedRouteRequirementsStorage(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "pg" {
				store = routePolicyPgStore(t)
			}
			acct, app, request, apiStore, saved := savedCheckFixture(t, store)
			body, _ := json.Marshal(saved)
			if strings.Contains(string(body), `"reason":"private"`) {
				t.Fatal("original rationale stored")
			}
			request.Requirements.Public[0].Reason = "another private explanation"
			unchanged, err := apiStore.SaveRouteRequirements(t.Context(), acct.ID, app.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &saved.Revision, Requirements: request.Requirements})
			if err != nil || unchanged.Revision != saved.Revision || !unchanged.UpdatedAt.Equal(saved.UpdatedAt) {
				t.Fatal("commentary changed saved policy revision")
			}
			zero := int64(0)
			if _, err := apiStore.SaveRouteRequirements(t.Context(), acct.ID, app.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &zero, Requirements: request.Requirements}); !errors.Is(err, state.ErrRouteRequirementsRevision) {
				t.Fatalf("stale revision: %v", err)
			}
			if _, err := apiStore.SaveRouteRequirements(t.Context(), acct.ID, app.ID, api.SaveRouteRequirementsRequest{Requirements: request.Requirements}); err == nil {
				t.Fatal("missing explicit revision accepted")
			}
			other, err := store.CreateAccount(t.Context(), "other-saved@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := apiStore.GetSavedRouteRequirements(t.Context(), other.ID, app.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign read: %v", err)
			}
			if _, err := apiStore.SaveRouteRequirements(t.Context(), other.ID, app.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &saved.Revision, Requirements: request.Requirements}); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign write: %v", err)
			}
			if _, err := apiStore.CheckRouteRequirements(t.Context(), other.ID, app.ID, api.CheckRouteRequirementsRequest{DeploymentID: request.DeploymentID}, testSavedRequirementsChecker(request.DeploymentID)); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign check: %v", err)
			}
			var writers sync.WaitGroup
			results := make(chan error, 2)
			for _, value := range []int64{700, 900} {
				writers.Go(func() {
					config, _, _ := routerequirements.NormalizeCoverage(request.Requirements)
					config.Groups[0].Require.Budget.MaxMS = &value
					_, err := apiStore.SaveRouteRequirements(t.Context(), acct.ID, app.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &saved.Revision, Requirements: config})
					results <- err
				})
			}
			writers.Wait()
			close(results)
			wins, conflicts := 0, 0
			for err := range results {
				if err == nil {
					wins++
				} else if errors.Is(err, state.ErrRouteRequirementsRevision) {
					conflicts++
				} else {
					t.Fatal(err)
				}
			}
			if wins != 1 || conflicts != 1 {
				t.Fatal("concurrent saves did not compare revisions")
			}
			current, err := apiStore.GetSavedRouteRequirements(t.Context(), acct.ID, app.ID)
			if err != nil || current.Revision != 2 || current.SHA256 == saved.SHA256 {
				t.Fatalf("replacement: %+v %v", current, err)
			}
			current.Requirements.Groups[0].Name = "mutated caller"
			readAgain, _ := apiStore.GetSavedRouteRequirements(t.Context(), acct.ID, app.ID)
			if readAgain.Requirements.Groups[0].Name == "mutated caller" {
				t.Fatal("returned requirements alias storage")
			}
			if err := store.DeleteApp(t.Context(), app.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := apiStore.GetSavedRouteRequirements(t.Context(), acct.ID, app.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("deleted app intent exposed: %v", err)
			}
			if _, err := apiStore.SaveRouteRequirements(t.Context(), acct.ID, app.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &current.Revision, Requirements: request.Requirements}); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("deleted app intent replaced: %v", err)
			}
		})
	}
}

func TestSavedRouteRequirementsChecks(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "pg" {
				store = routePolicyPgStore(t)
			}
			acct, app, policy, apiStore, saved := savedCheckFixture(t, store)
			request := api.CheckRouteRequirementsRequest{DeploymentID: policy.DeploymentID, ExpectedRevision: &saved.Revision}
			checker := testSavedRequirementsChecker(request.DeploymentID)
			before, err := apiStore.CheckRouteRequirements(t.Context(), acct.ID, app.ID, request, checker)
			if err != nil || before.Report.Status != "violated" {
				t.Fatalf("budget violation: %+v %v", before, err)
			}
			planner := routeGroupTestPlanner(policy.RoutePolicyPlanRequest)
			tx := store.(state.RoutePolicyStore)
			plan, err := tx.PlanRoutePolicy(t.Context(), acct.ID, app.ID, policy.RoutePolicyPlanRequest, planner)
			if err != nil {
				t.Fatal(err)
			}
			policy.ExpectedPlanSHA256 = plan.SHA256
			if _, _, err := tx.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "saved-check-fix", policy, planner); err != nil {
				t.Fatal(err)
			}
			after, err := apiStore.CheckRouteRequirements(t.Context(), acct.ID, app.ID, request, checker)
			if err != nil || after.Report.Status != "satisfied" || after.ConfigurationSHA256 == before.ConfigurationSHA256 || after.RequirementsRevision != before.RequirementsRevision {
				t.Fatalf("fixed policy: %+v %v", after, err)
			}
			newDoc := []byte(`{"openapi":"3.1.0","paths":{"/checkout/{id}":{"post":{}},"/health":{"get":{}},"/export":{"get":{}}}}`)
			if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), policy.DeploymentID, acct.ID, app.ID, newDoc, "manual_upload", false); err != nil {
				t.Fatal(err)
			}
			newRoute, err := apiStore.CheckRouteRequirements(t.Context(), acct.ID, app.ID, request, checker)
			if err != nil || newRoute.Report.Status != "violated" || newRoute.Report.Coverage.SHA256 == after.Report.Coverage.SHA256 {
				t.Fatalf("uncovered new capture: %+v %v", newRoute, err)
			}
			stale := int64(2)
			request.ExpectedRevision = &stale
			if _, err := apiStore.CheckRouteRequirements(t.Context(), acct.ID, app.ID, request, checker); !errors.Is(err, state.ErrRouteRequirementsRevision) {
				t.Fatalf("revision pin ignored: %v", err)
			}
			request.ExpectedRevision = nil
			if err := store.DeleteDeploymentOpenAPIDoc(t.Context(), policy.DeploymentID, acct.ID); err != nil {
				t.Fatal(err)
			}
			missing, err := apiStore.CheckRouteRequirements(t.Context(), acct.ID, app.ID, request, checker)
			if err != nil || missing.Report.Status != "unknown" || missing.Report.Coverage.Status != "unavailable" {
				t.Fatalf("missing capture passed: %+v %v", missing, err)
			}
		})
	}
}
