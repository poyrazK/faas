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

func TestLifecycleApprovalStoreAndGate(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		for _, scenario := range []string{"valid", "candidate_recaptured", "baseline_recaptured", "capture_restored", "gate_changed", "requirements_changed", "configuration_changed", "metadata_removed", "extra_baseline"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				if backend == "pg" {
					store = routePolicyPgStore(t)
				}
				acct, app, baseline, candidate := routeGateFixture(t, store)
				saved := saveGateIntent(t, store, acct, app)
				zero := int64(0)
				old := `{"openapi":"3.1.0","paths":{"/health":{"get":{"deprecated":true,"x-gregale-deprecated-at":"2026-10-01T00:00:00Z","x-gregale-sunset-at":"2099-01-01T00:00:00Z","x-gregale-successor":"https://canary-gate.gregale.dev/old"}}}}`
				proposed := strings.ReplaceAll(old, "https://canary-gate.gregale.dev/old", "https://canary-gate.gregale.dev/health")
				capture := func(id, doc string) {
					t.Helper()
					if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), id, acct.ID, app.ID, []byte(doc), "manual_upload", false); err != nil {
						t.Fatal(err)
					}
				}
				capture(baseline.ID, old)
				capture(candidate.ID, proposed)
				if scenario == "extra_baseline" {
					extra, err := store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:extra", TrafficPercent: 1, TrafficPercentExplicit: true})
					if err != nil {
						t.Fatal(err)
					}
					capture(extra.ID, old)
					if err = store.MarkDeploymentLive(t.Context(), extra.ID); err != nil {
						t.Fatal(err)
					}
				}
				gate, err := store.(state.CanaryRouteGateStore).SetCanaryRouteGate(t.Context(), acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &zero})
				if err != nil {
					t.Fatal(err)
				}

				checker := store.(state.RouteRequirementsStore)
				check, err := checker.CheckRouteRequirements(t.Context(), acct.ID, app.ID, api.CheckRouteRequirementsRequest{DeploymentID: candidate.ID}, testSavedRequirementsChecker(candidate.ID))
				if err != nil {
					t.Fatal(err)
				}
				_, bmeta, _ := store.GetDeploymentOpenAPIDoc(t.Context(), baseline.ID, acct.ID)
				_, cmeta, _ := store.GetDeploymentOpenAPIDoc(t.Context(), candidate.ID, acct.ID)
				request := api.ApproveRouteLifecycleRequest{ExpectedGateRevision: &gate.Revision, ExpectedRequirementsRevision: &saved.Revision, ExpectedRemovalPolicyRevision: &zero, ConfigurationSHA256: check.ConfigurationSHA256, BaselineDeploymentID: baseline.ID, CandidateDeploymentID: candidate.ID, BaselineContractSHA256: fmt.Sprintf("%x", bmeta.DocSHA256), CandidateContractSHA256: fmt.Sprintf("%x", cmeta.DocSHA256), Mappings: []api.RouteLifecycleMapping{{Method: "get", Path: "/health", SuccessorMethod: "get", SuccessorPath: "/health", SuccessorURL: "https://canary-gate.gregale.dev/health"}}}
				approvals := store.(state.RouteLifecycleApprovalStore)
				validator := func(state.RoutePolicySnapshot, *state.RoutePolicyContract, *state.RoutePolicyContract, []api.RouteLifecycleMapping) error {
					return nil
				}
				receipt, err := approvals.ApproveRouteLifecycle(t.Context(), acct.ID, app.ID, "owner:test", request, automaticCheckFingerprint, validator)
				if err != nil {
					t.Fatal(err)
				}
				originalID := receipt.ID
				receipt.Mappings[0].SuccessorPath = "/forged"
				persisted, err := approvals.GetRouteLifecycleApproval(t.Context(), acct.ID, app.ID, originalID)
				if err != nil || persisted.Mappings[0].SuccessorPath != "/health" || persisted.Compatibility != "no_supported_breaks" || persisted.ApprovedBy != "owner:test" {
					t.Fatalf("persisted %+v %v", persisted, err)
				}
				other, err := store.CreateAccount(t.Context(), "other-lifecycle@example.test", api.PlanPro)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := approvals.GetRouteLifecycleApproval(t.Context(), other.ID, app.ID, originalID); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("foreign read %v", err)
				}
				changed := request
				changed.ConfigurationSHA256 = strings.Repeat("0", 64)
				if _, err := approvals.ApproveRouteLifecycle(t.Context(), acct.ID, app.ID, "owner:test", changed, automaticCheckFingerprint, validator); !errors.Is(err, state.ErrRouteLifecycleReviewChanged) {
					t.Fatalf("stale request %v", err)
				}
				changed = request
				changed.Mappings = append([]api.RouteLifecycleMapping(nil), request.Mappings...)
				changed.Mappings[0].SuccessorURL = "https://canary-gate.gregale.dev/other"
				changed.Mappings[0].SuccessorPath = "/other"
				if _, err := approvals.ApproveRouteLifecycle(t.Context(), acct.ID, app.ID, "owner:test", changed, automaticCheckFingerprint, validator); err == nil {
					t.Fatal("unbound mapping approved")
				}
				rejection := errors.New("incompatible")
				if _, err := approvals.ApproveRouteLifecycle(t.Context(), acct.ID, app.ID, "owner:test", request, automaticCheckFingerprint, func(state.RoutePolicySnapshot, *state.RoutePolicyContract, *state.RoutePolicyContract, []api.RouteLifecycleMapping) error {
					return rejection
				}); !errors.Is(err, rejection) {
					t.Fatalf("validator rejection %v", err)
				}
				switch scenario {
				case "candidate_recaptured":
					capture(candidate.ID, proposed)
				case "baseline_recaptured":
					capture(baseline.ID, old)
				case "capture_restored":
					capture(candidate.ID, gateContract)
					capture(candidate.ID, proposed)
				case "metadata_removed":
					capture(candidate.ID, gateContract)
				case "gate_changed":
					gate, err = store.(state.CanaryRouteGateStore).SetCanaryRouteGate(t.Context(), acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "report", ExpectedRevision: &gate.Revision})
					if err != nil {
						t.Fatal(err)
					}
					_, err = store.(state.CanaryRouteGateStore).SetCanaryRouteGate(t.Context(), acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &gate.Revision})
					if err != nil {
						t.Fatal(err)
					}
				case "requirements_changed":
					saved.Requirements.Public = append(saved.Requirements.Public, api.RoutePublicException{Method: "POST", Path: "/future", Reason: "new intent"})
					if _, err = checker.SaveRouteRequirements(t.Context(), acct.ID, app.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &saved.Revision, Requirements: saved.Requirements}); err != nil {
						t.Fatal(err)
					}
				case "configuration_changed":
					mode := api.ConsumerAuthModeRequired
					if _, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetConsumerAuthMode: true, ConsumerAuthMode: &mode}); err != nil {
						t.Fatal(err)
					}

				}
				if scenario == "candidate_recaptured" || scenario == "baseline_recaptured" || scenario == "capture_restored" || scenario == "metadata_removed" {
					invalid, err := approvals.GetRouteLifecycleApproval(t.Context(), acct.ID, app.ID, originalID)
					if err != nil || invalid.InvalidatedAt == nil {
						t.Fatalf("capture receipt revived %+v %v", invalid, err)
					}
				}
				if err = store.(state.AutomaticRouteCheckStore).QueueAutomaticRouteCheck(t.Context(), acct.ID, app.ID, candidate.ID); err != nil {
					t.Fatal(err)
				}
				claim, err := claimCandidateRouteCheck(t, store, candidate.ID)
				if err != nil || !finishAutomaticCheck(t, store, claim) {
					t.Fatal("candidate check", err)
				}
				var decision api.RouteGateDecision
				_, _, err = store.(state.CanaryAdvancer).AdvanceCanary(t.Context(), candidate.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteCheckFingerprint: automaticCheckFingerprint, RouteGateDecision: &decision, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "receipt-test"}})
				if scenario == "valid" {
					if err != nil || len(decision.LifecycleApprovalIDs) != 1 || decision.LifecycleApprovalIDs[0] != originalID {
						t.Fatalf("valid receipt not consumed %+v %v", decision, err)
					}
				} else {
					var blocked *state.RouteGateBlockedError
					if !errors.As(err, &blocked) {
						t.Fatalf("stale receipt allowed %+v %v", decision, err)
					}
					found := false
					for _, reason := range decision.Reasons {
						if strings.HasPrefix(reason, "lifecycle_") {
							found = true
						}
					}
					if !found {
						t.Fatalf("not blocked by lifecycle %+v", decision)
					}
				}
			})
		}
	}
}
