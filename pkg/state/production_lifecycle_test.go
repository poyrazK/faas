package state_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductionLifecycleTransitions(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		for _, action := range []string{"ordinary_activation", "initial_canary", "initial_split", "manual_traffic", "unchanged", "report", "receipt", "stale_receipt", "automatic_recovery"} {
			t.Run(backend+"/"+action, func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				if backend == "pg" {
					store = routePolicyPgStore(t)
				}
				acct, err := store.CreateAccount(t.Context(), "production-lifecycle@example.test", api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "production-lifecycle", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
				if err != nil {
					t.Fatal(err)
				}
				stable, err := store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stable"})
				if err != nil {
					t.Fatal(err)
				}
				if err = store.MarkDeploymentLive(t.Context(), stable.ID); err != nil {
					t.Fatal(err)
				}
				old := `{"openapi":"3.1.0","paths":{"/health":{"get":{"deprecated":true,"x-gregale-deprecated-at":"2026-10-01T00:00:00Z","x-gregale-sunset-at":"2099-01-01T00:00:00Z","x-gregale-successor":"https://production-lifecycle.gregale.dev/old"}}}}`
				next := gateContract
				if action == "unchanged" {
					next = old
				}
				if action == "receipt" || action == "stale_receipt" {
					next = strings.ReplaceAll(old, "gregale.dev/old", "gregale.dev/health")
				}
				capture := func(id, doc string) {
					t.Helper()
					if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), id, acct.ID, app.ID, []byte(doc), "manual_upload", false); err != nil {
						t.Fatal(err)
					}
				}
				capture(stable.ID, old)
				saved := saveGateIntent(t, store, acct, app)
				gate := api.CanaryRouteGate{}
				if action != "report" {
					zero := int64(0)
					gate, err = store.(state.CanaryRouteGateStore).SetCanaryRouteGate(t.Context(), acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &zero})
					if err != nil {
						t.Fatal(err)
					}
				}
				dep := state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate"}
				if action == "initial_canary" {
					dep.CanaryPreset = "balanced"
					dep.CanaryTotalSteps = 4
					dep.RolloutState = "pending"
					dep.TrafficPercent = 1
				}
				if action == "initial_split" {
					dep.TrafficPercentExplicit = true
					dep.TrafficPercent = 10
				}
				if action == "manual_traffic" {
					dep.TrafficPercentExplicit = true
					dep.TrafficPercent = 0
				}
				candidate, err := store.CreateDeployment(t.Context(), dep)
				if err != nil {
					t.Fatal(err)
				}
				if action == "manual_traffic" {
					if err = store.MarkDeploymentLive(t.Context(), candidate.ID); err != nil {
						t.Fatalf("dark staging blocked: %v", err)
					}
				}
				capture(candidate.ID, next)
				if action == "receipt" || action == "stale_receipt" {
					check, err := store.(state.RouteRequirementsStore).CheckRouteRequirements(t.Context(), acct.ID, app.ID, api.CheckRouteRequirementsRequest{DeploymentID: candidate.ID}, testSavedRequirementsChecker(candidate.ID))
					if err != nil {
						t.Fatal(err)
					}
					_, bm, _ := store.GetDeploymentOpenAPIDoc(t.Context(), stable.ID, acct.ID)
					_, cm, _ := store.GetDeploymentOpenAPIDoc(t.Context(), candidate.ID, acct.ID)
					zero := int64(0)
					request := api.ApproveRouteLifecycleRequest{ExpectedGateRevision: &gate.Revision, ExpectedRequirementsRevision: &saved.Revision, ExpectedRemovalPolicyRevision: &zero, ConfigurationSHA256: check.ConfigurationSHA256, BaselineDeploymentID: stable.ID, CandidateDeploymentID: candidate.ID, BaselineContractSHA256: fmt.Sprintf("%x", bm.DocSHA256), CandidateContractSHA256: fmt.Sprintf("%x", cm.DocSHA256), Mappings: []api.RouteLifecycleMapping{{Method: "GET", Path: "/health", SuccessorMethod: "GET", SuccessorPath: "/health", SuccessorURL: "https://production-lifecycle.gregale.dev/health"}}}
					_, err = store.(state.RouteLifecycleApprovalStore).ApproveRouteLifecycle(t.Context(), acct.ID, app.ID, "owner:test", request, automaticCheckFingerprint, func(state.RoutePolicySnapshot, *state.RoutePolicyContract, *state.RoutePolicyContract, []api.RouteLifecycleMapping) error {
						return nil
					})
					if err != nil {
						t.Fatal(err)
					}
					if action == "stale_receipt" {
						mode := api.ConsumerAuthModeRequired
						if _, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetConsumerAuthMode: true, ConsumerAuthMode: &mode}); err != nil {
							t.Fatal(err)
						}
					}
				}
				if action == "automatic_recovery" {
					// A clear cutover can be rolled back even when the current capture has
					// since acquired a lifecycle promise that the retained release lacks.
					capture(candidate.ID, old)
					if err = store.MarkDeploymentLive(t.Context(), candidate.ID); err != nil {
						t.Fatal(err)
					}
					capture(stable.ID, gateContract)
					got, err := store.AutoRollbackDeploymentsTx(t.Context(), app.ID, candidate.ID)
					if err != nil || got != stable.ID {
						t.Fatalf("recovery: %s %v", got, err)
					}
					page, e := store.(state.RouteLifecycleHistoryStore).ListRouteLifecycleHistory(t.Context(), acct.ID, app.ID, 1, "")
					if e != nil || len(page.Entries) != 1 || page.Entries[0].DeploymentID != stable.ID || !page.Entries[0].Recovery || page.Entries[0].Outcome != "applied" {
						t.Fatalf("recovery history %+v %v", page, e)
					}
					return
				}
				if action == "manual_traffic" {
					_, err = store.UpdateDeploymentTraffic(t.Context(), candidate.ID, 10)
				} else {
					err = store.MarkDeploymentLive(t.Context(), candidate.ID)
				}
				allowed := action == "unchanged" || action == "report" || action == "receipt"
				page, historyErr := store.(state.RouteLifecycleHistoryStore).ListRouteLifecycleHistory(t.Context(), acct.ID, app.ID, 1, "")
				if historyErr != nil || len(page.Entries) != 1 || page.Entries[0].DeploymentID != candidate.ID || !page.Entries[0].EvidenceAvailable {
					t.Fatalf("history %+v %v", page, historyErr)
				}
				expectedOutcome := "blocked"
				if allowed {
					expectedOutcome = "applied"
				}
				if page.Entries[0].Outcome != expectedOutcome {
					t.Fatalf("outcome %+v", page)
				}
				if action == "receipt" {
					if len(page.Entries[0].Approvals) != 1 || !page.Entries[0].Approvals[0].Used || page.Entries[0].Approvals[0].Status != "valid" {
						t.Fatalf("approval history %+v", page)
					}
				}
				if action == "stale_receipt" {
					if len(page.Entries[0].Approvals) != 1 || page.Entries[0].Approvals[0].Status != "invalidated" {
						t.Fatalf("stale history %+v", page)
					}
				}
				history := store.(state.RouteLifecycleHistoryStore)
				if _, e := history.ListRouteLifecycleHistory(t.Context(), "00000000-0000-4000-8000-000000000001", app.ID, 1, ""); !errors.Is(e, state.ErrNotFound) {
					t.Fatalf("foreign history %v", e)
				}
				if page.NextCursor != "" {
					older, e := history.ListRouteLifecycleHistory(t.Context(), acct.ID, app.ID, 1, page.NextCursor)
					if e != nil || len(older.Entries) != 1 || older.Entries[0].ID == page.Entries[0].ID {
						t.Fatalf("pagination %+v %v", older, e)
					}
				}
				for _, bad := range []string{"0", "01", "-1", "abc", "9223372036854775808"} {
					if _, e := history.ListRouteLifecycleHistory(t.Context(), acct.ID, app.ID, 1, bad); !errors.Is(e, state.ErrInvalidArgument) {
						t.Fatalf("cursor %s: %v", bad, e)
					}
				}

				if allowed {
					if err != nil {
						t.Fatalf("allowed transition blocked: %v", err)
					}
				} else {
					var blocked *state.RouteGateBlockedError
					if !errors.As(err, &blocked) {
						t.Fatalf("expected lifecycle rejection, got %v", err)
					}
					after, _ := store.DeploymentByID(t.Context(), stable.ID)
					if after.Status != state.DeployLive || after.TrafficPercent != 100 {
						t.Fatalf("baseline mutated: %+v", after)
					}
				}
			})
		}
	}
}

func TestProductionLifecycleCheckedRollback(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		for _, service := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/service=%v", backend, service), func(t *testing.T) {
				var s state.Store = state.NewMemStore()
				if backend == "pg" {
					s = routePolicyPgStore(t)
				}
				acct, app, target, current := checkedRollbackFixture(t, s, service)
				promised := `{"openapi":"3.1.0","paths":{"/health":{"get":{"deprecated":true,"x-gregale-deprecated-at":"2026-10-01T00:00:00Z","x-gregale-sunset-at":"2099-01-01T00:00:00Z","x-gregale-successor":"https://example.test/health"}}}}`
				for id, doc := range map[string]string{current.ID: promised, target.ID: gateContract} {
					if err := s.UpsertDeploymentOpenAPIDoc(t.Context(), id, acct.ID, app.ID, []byte(doc), "manual_upload", false); err != nil {
						t.Fatal(err)
					}
				}
				saveGateIntent(t, s, acct, app)
				zero := int64(0)
				if _, err := s.(state.CanaryRouteGateStore).SetCanaryRouteGate(t.Context(), acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
					t.Fatal(err)
				}
				rollback := s.(state.CheckedRollbackStore)
				operation, err := rollback.CreateCheckedRollback(t.Context(), acct.ID, app.ID, target.ID, current.ID, "restore retained release")
				if err != nil {
					t.Fatal(err)
				}
				if err = s.MarkDeploymentLive(t.Context(), target.ID); err != nil {
					t.Fatalf("dark rollback readiness blocked: %v", err)
				}
				operation, err = rollback.GetCheckedRollback(t.Context(), acct.ID, app.ID, operation.ID)
				if err != nil || operation.Status != "ready" {
					t.Fatalf("readiness: %+v %v", operation, err)
				}
				_, err = rollback.CommitCheckedRollback(t.Context(), operation)
				var blocked *state.RouteGateBlockedError
				if !errors.As(err, &blocked) {
					t.Fatalf("checked rollback bypassed lifecycle: %v", err)
				}
				live, _ := s.DeploymentByID(t.Context(), current.ID)
				dark, _ := s.DeploymentByID(t.Context(), target.ID)
				if live.Status != state.DeployLive || live.TrafficPercent != 100 || dark.TrafficPercent != 0 {
					t.Fatalf("routing mutated: %+v %+v", live, dark)
				}
			})
		}
	}
}
