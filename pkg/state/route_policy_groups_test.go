package state_test

// ADR-446: captured route-group planning, impact and transactional verification.

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/routerequirements"
	"github.com/onebox-faas/faas/pkg/state"
)

const routeGroupContract = `{"openapi":"3.1.0","info":{"title":"sample","version":"1"},"paths":{"/checkout/{id}":{"post":{"responses":{"200":{"description":"ok"}}}},"/health":{"get":{"responses":{"200":{"description":"ok"}}}}}}`

func routeGroupFixture(t *testing.T, store state.Store) (state.Account, state.App, api.RoutePolicyApplyRequest, state.RoutePolicyPlanner) {
	t.Helper()
	acct, app, _, _ := routePolicyFixture(t, store)
	deployment, err := store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:group-policy", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), deployment.ID, acct.ID, app.ID, []byte(routeGroupContract), "manual_upload", false); err != nil {
		t.Fatal(err)
	}
	config, err := routerequirements.ParsePreview([]byte(`{"version":2,"groups":[{"name":"checkout","path_prefix":"/checkout/","methods":["POST"],"require":{"throttle":{"key_by":"none","max_rps":1},"budget":{"explicit":true,"max_ms":500}}}],"public":[{"method":"GET","path":"/health","reason":"private"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	request := api.RoutePolicyApplyRequest{RoutePolicyPlanRequest: api.RoutePolicyPlanRequest{Requirements: config, DeploymentID: deployment.ID, ThrottleBurst: 10}, Confirm: true}
	planner := routeGroupTestPlanner(request.RoutePolicyPlanRequest)
	plan, err := store.(state.RoutePolicyStore).PlanRoutePolicy(t.Context(), acct.ID, app.ID, request.RoutePolicyPlanRequest, planner)
	if err != nil || plan.Status != "ready" || plan.Version != 3 {
		t.Fatalf("group plan: %+v %v", plan, err)
	}
	request.ExpectedPlanSHA256 = plan.SHA256
	return acct, app, request, planner
}

func routeGroupTestPlanner(request api.RoutePolicyPlanRequest) state.RoutePolicyPlanner {
	return func(snapshot state.RoutePolicySnapshot) (api.RoutePolicyPlan, error) {
		context := routerequirements.Context{Host: snapshot.App.Slug + ".gregale.dev", Rules: snapshot.Rules, App: api.AppResponse{ID: snapshot.App.ID, Slug: snapshot.App.Slug, EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 10000, RequestBudgetMaxMS: 20000}}}
		inventory := routerequirements.CoverageInventory{Status: "unavailable", Code: "candidate_inventory_unavailable", Source: "captured_candidate_contract"}
		if contract := snapshot.Contract; contract != nil {
			inventory.Deployment, inventory.SHA256 = contract.DeploymentID, contract.SHA256
			if len(contract.Doc) > 0 && !contract.Truncated {
				spec, err := openapidiff.LoadBytes(contract.Doc)
				if err != nil {
					return api.RoutePolicyPlan{}, err
				}
				inventory = routerequirements.CandidateInventory(spec, contract.DeploymentID, contract.SHA256)
			}
		}
		config := request.Requirements
		options := routerequirements.PlanOptions{PlanName: string(snapshot.Account.Plan), ThrottleBurst: request.ThrottleBurst, ConsolidateBudgets: request.ConsolidateBudgets}
		if request.Saved {
			if snapshot.SavedRequirements == nil {
				return api.RoutePolicyPlan{}, state.ErrNotFound
			}
			config = snapshot.SavedRequirements.Requirements
			options.RequirementsRevision = snapshot.SavedRequirements.Revision
		}
		return routerequirements.BuildServerGroupPlan(config, context, options, inventory)
	}
}

func TestRouteGroupTransactionsBindContractAndReceipt(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		for _, scenario := range []string{"apply and replay after capture deletion", "changed contract", "truncated contract", "missing contract", "wrong app deployment", "verification rollback"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				if backend == "pg" {
					store = routePolicyPgStore(t)
				}
				acct, app, request, planner := routeGroupFixture(t, store)
				tx := store.(state.RoutePolicyStore)
				switch scenario {
				case "changed contract":
					changed := map[string]any{}
					_ = json.Unmarshal([]byte(routeGroupContract), &changed)
					changed["description"] = "new capture"
					body, _ := json.Marshal(changed)
					if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), request.DeploymentID, acct.ID, app.ID, body, "manual_upload", false); err != nil {
						t.Fatal(err)
					}
				case "truncated contract":
					if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), request.DeploymentID, acct.ID, app.ID, []byte(routeGroupContract), "manual_upload", true); err != nil {
						t.Fatal(err)
					}
				case "missing contract":
					if err := store.DeleteDeploymentOpenAPIDoc(t.Context(), request.DeploymentID, acct.ID); err != nil {
						t.Fatal(err)
					}
				case "wrong app deployment":
					other, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "other-group", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
					if err != nil {
						t.Fatal(err)
					}
					deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: other.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:other", Status: state.DeployLive})
					if err != nil {
						t.Fatal(err)
					}
					request.DeploymentID = deployment.ID
				case "verification rollback":
					original := planner
					calls := 0
					planner = func(snapshot state.RoutePolicySnapshot) (api.RoutePolicyPlan, error) {
						calls++
						if calls == 2 {
							return api.RoutePolicyPlan{}, errors.New("forced verification failure")
						}
						return original(snapshot)
					}
				}
				receipt, replayed, err := tx.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "group", request, planner)
				if scenario == "apply and replay after capture deletion" {
					if err != nil || replayed || receipt.Verification.Status != "satisfied" || receipt.Verification.Version != 2 || receipt.Verification.Coverage.Deployment != request.DeploymentID || len(receipt.Changes) != 2 {
						t.Fatalf("receipt=%+v replay=%t err=%v", receipt, replayed, err)
					}
					if err := store.DeleteDeploymentOpenAPIDoc(t.Context(), request.DeploymentID, acct.ID); err != nil {
						t.Fatal(err)
					}
					second, replayed, err := tx.ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "group", request, planner)
					if err != nil || !replayed || second.ID != receipt.ID {
						t.Fatalf("recovery lost: %+v %t %v", second, replayed, err)
					}
					return
				}
				if err == nil {
					t.Fatal("unreviewed group transaction accepted")
				}
				if scenario == "wrong app deployment" && !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("ownership error: %v", err)
				}
				if scenario == "changed contract" && !errors.Is(err, state.ErrRoutePolicyStale) {
					t.Fatalf("capture mutation error: %v", err)
				}
				rules, _ := store.ListEdgeRulesForApp(t.Context(), app.ID)
				if len(rules) != 0 {
					t.Fatal("failed group transaction left changes")
				}
				if _, err := tx.FindRoutePolicyReceipt(t.Context(), acct.ID, app.ID, "group", request); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("failed transaction left receipt: %v", err)
				}
			})
		}
	}
}

func TestRouteGroupApplyLocksConcurrentCaptureWriter(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "pg" {
				store = routePolicyPgStore(t)
			}
			acct, app, request, original := routeGroupFixture(t, store)
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
				_, _, err := store.(state.RoutePolicyStore).ApplyRoutePolicy(t.Context(), acct.ID, app.ID, "capture-lock", request, planner)
				applied <- err
			}()
			<-entered
			updated := make(chan error, 1)
			go func() {
				updated <- store.UpsertDeploymentOpenAPIDoc(t.Context(), request.DeploymentID, acct.ID, app.ID, append([]byte(routeGroupContract), ' '), "manual_upload", false)
			}()
			var early error
			writerFinished := false
			select {
			case early = <-updated:
				writerFinished = true
			case <-time.After(50 * time.Millisecond):
			}
			close(release)
			if err := <-applied; err != nil {
				t.Fatalf("apply: %v", err)
			}
			if writerFinished {
				t.Fatalf("capture writer escaped transaction lock: %v", early)
			}
			if err := <-updated; err != nil {
				t.Fatalf("capture update: %v", err)
			}
		})
	}
}
