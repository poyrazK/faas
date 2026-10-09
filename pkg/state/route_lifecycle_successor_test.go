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

func TestLifecycleCrossAppSuccessors(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		for _, scenario := range []string{"canonical", "custom_domain", "target_recaptured", "target_restored", "domain_deleted", "configuration_restored", "ambiguous", "foreign", "wrong_hash"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				if backend == "pg" {
					store = routePolicyPgStore(t)
				}
				ctx := t.Context()
				acct, app, baseline, candidate := routeGateFixture(t, store)
				saved := saveGateIntent(t, store, acct, app)
				zero := int64(0)
				owner := acct.ID
				if scenario == "foreign" {
					other, err := store.CreateAccount(ctx, "successor-other@example.test", api.PlanPro)
					if err != nil {
						t.Fatal(err)
					}
					owner = other.ID
				}
				target, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: owner, Slug: "successor", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
				if err != nil {
					t.Fatal(err)
				}
				deployment, err := store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: target.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:successor"})
				if err != nil {
					t.Fatal(err)
				}
				if err = store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
					t.Fatal(err)
				}
				targetDoc := `{"openapi":"3.1.0","paths":{"/next":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
				if err = store.UpsertDeploymentOpenAPIDoc(ctx, deployment.ID, owner, target.ID, []byte(targetDoc), "manual_upload", false); err != nil {
					t.Fatal(err)
				}
				host := "successor.gregale.dev"
				if scenario == "custom_domain" || scenario == "domain_deleted" {
					host = "successor.example.test"
					if _, err = store.CreateCustomDomain(ctx, host, target.ID, "challenge"); err != nil {
						t.Fatal(err)
					}
					if err = store.MarkDomainVerified(ctx, host); err != nil {
						t.Fatal(err)
					}
				}
				old := `{"openapi":"3.1.0","paths":{"/health":{"get":{"deprecated":true,"x-gregale-deprecated-at":"2026-10-01T00:00:00Z","x-gregale-sunset-at":"2099-01-01T00:00:00Z","x-gregale-successor":"https://canary-gate.gregale.dev/old","responses":{"200":{"description":"ok"}}}}}}`
				proposed := strings.ReplaceAll(old, "https://canary-gate.gregale.dev/old", "https://"+host+"/next")
				for id, doc := range map[string]string{baseline.ID: old, candidate.ID: proposed} {
					if err = store.UpsertDeploymentOpenAPIDoc(ctx, id, acct.ID, app.ID, []byte(doc), "manual_upload", false); err != nil {
						t.Fatal(err)
					}
				}
				gate, err := store.(state.CanaryRouteGateStore).SetCanaryRouteGate(ctx, acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &zero})
				if err != nil {
					t.Fatal(err)
				}
				check, err := store.(state.RouteRequirementsStore).CheckRouteRequirements(ctx, acct.ID, app.ID, api.CheckRouteRequirementsRequest{DeploymentID: candidate.ID}, testSavedRequirementsChecker(candidate.ID))
				if err != nil {
					t.Fatal(err)
				}
				_, bm, _ := store.GetDeploymentOpenAPIDoc(ctx, baseline.ID, acct.ID)
				_, cm, _ := store.GetDeploymentOpenAPIDoc(ctx, candidate.ID, acct.ID)
				_, tm, _ := store.GetDeploymentOpenAPIDoc(ctx, deployment.ID, owner)
				mapping := api.RouteLifecycleMapping{Method: "get", Path: "/health", SuccessorMethod: "get", SuccessorPath: "/next", SuccessorURL: "https://" + host + "/next", SuccessorAppID: target.ID, SuccessorDeploymentID: deployment.ID, SuccessorContractSHA256: fmt.Sprintf("%x", tm.DocSHA256)}
				request := api.ApproveRouteLifecycleRequest{ExpectedGateRevision: &gate.Revision, ExpectedRequirementsRevision: &saved.Revision, ExpectedRemovalPolicyRevision: &zero, ConfigurationSHA256: check.ConfigurationSHA256, BaselineDeploymentID: baseline.ID, CandidateDeploymentID: candidate.ID, BaselineContractSHA256: fmt.Sprintf("%x", bm.DocSHA256), CandidateContractSHA256: fmt.Sprintf("%x", cm.DocSHA256), Mappings: []api.RouteLifecycleMapping{mapping}}
				if scenario == "wrong_hash" {
					request.Mappings[0].SuccessorContractSHA256 = strings.Repeat("0", 64)
				}
				if scenario == "ambiguous" {
					extra, err := store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: target.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:split", TrafficPercent: 1, TrafficPercentExplicit: true})
					if err != nil {
						t.Fatal(err)
					}
					if err = store.MarkDeploymentLive(ctx, extra.ID); err != nil {
						t.Fatal(err)
					}
				}
				validator := func(snapshot state.RoutePolicySnapshot, _, _ *state.RoutePolicyContract, mappings []api.RouteLifecycleMapping) error {
					entry := snapshot.LifecycleSuccessors["GET /health"]
					if entry.Snapshot.App.ID != target.ID || entry.Snapshot.Contract.DeploymentID != deployment.ID {
						t.Fatal("wrong resolved destination")
					}
					if scenario == "custom_domain" && !entry.VerifiedDomain {
						t.Fatal("verified domain missing")
					}
					return nil
				}
				approvals := store.(state.RouteLifecycleApprovalStore)
				receipt, err := approvals.ApproveRouteLifecycle(ctx, acct.ID, app.ID, "owner:test", request, automaticCheckFingerprint, validator)
				if scenario == "foreign" || scenario == "ambiguous" || scenario == "wrong_hash" {
					if err == nil {
						t.Fatal("unsafe successor approved")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "target_recaptured", "target_restored":
					if scenario == "target_restored" {
						if err = store.UpsertDeploymentOpenAPIDoc(ctx, deployment.ID, owner, target.ID, []byte(gateContract), "manual_upload", false); err != nil {
							t.Fatal(err)
						}
					}
					err = store.UpsertDeploymentOpenAPIDoc(ctx, deployment.ID, owner, target.ID, []byte(targetDoc), "manual_upload", false)
				case "domain_deleted":
					err = store.DeleteCustomDomain(ctx, host)
				case "configuration_restored":
					for _, mode := range []string{api.ConsumerAuthModeRequired, api.ConsumerAuthModeOptional} {
						_, err = store.UpdateApp(ctx, target.ID, state.UpdateAppParams{SetConsumerAuthMode: true, ConsumerAuthMode: &mode})
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if err = store.(state.AutomaticRouteCheckStore).QueueAutomaticRouteCheck(ctx, acct.ID, app.ID, candidate.ID); err != nil {
					t.Fatal(err)
				}
				claim, err := claimCandidateRouteCheck(t, store, candidate.ID)
				if err != nil || !finishAutomaticCheck(t, store, claim) {
					t.Fatal(err)
				}
				var decision api.RouteGateDecision
				_, _, err = store.(state.CanaryAdvancer).AdvanceCanary(ctx, candidate.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteCheckFingerprint: automaticCheckFingerprint, RouteGateDecision: &decision, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "successor-test"}})
				if scenario == "canonical" || scenario == "custom_domain" {
					if err != nil || len(decision.LifecycleApprovalIDs) != 1 || decision.LifecycleApprovalIDs[0] != receipt.ID {
						t.Fatalf("valid %+v %v", decision, err)
					}
				} else {
					var blocked *state.RouteGateBlockedError
					if !errors.As(err, &blocked) {
						t.Fatalf("stale successor allowed %+v %v", decision, err)
					}
					persisted, e := approvals.GetRouteLifecycleApproval(ctx, acct.ID, app.ID, receipt.ID)
					if e != nil || persisted.InvalidatedAt == nil {
						t.Fatalf("receipt not permanently invalidated %+v %v", persisted, e)
					}
				}
			})
		}
	}
}

func TestLifecycleProjectGraphSuccessors(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		for _, scenario := range []string{"graph", "graph_zero_weight", "promoted", "rolled_back", "wrong_member", "frozen_settings", "missing_graph"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				if backend == "pg" {
					store = routePolicyPgStore(t)
				}
				ctx := t.Context()
				acct, app, baseline, candidate := routeGateFixture(t, store)
				saved := saveGateIntent(t, store, acct, app)
				zero := int64(0)
				owner := acct.ID
				project, err := store.CreateProject(ctx, state.Project{AccountID: owner, Slug: "successor-project"})
				if err != nil {
					t.Fatal(err)
				}
				target, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: owner, ProjectID: project.ID, Slug: "successor", Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}, Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
				if err != nil {
					t.Fatal(err)
				}
				target.RetryPolicyJSON = []byte(`{"max_attempts":2,"backoff_multiplier":1e0}`)
				settings, err := state.WorkloadSettingsFromApp(target)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = store.(state.ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, owner, project.ID, "production", target.ID, 0, settings); err != nil {
					t.Fatal(err)
				}
				deployment, err := store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: target.ID, Kind: state.DeploymentKindImage, Scope: "production", ImageDigest: "sha256:successor"})
				if err != nil {
					t.Fatal(err)
				}
				if err = store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
					t.Fatal(err)
				}
				targetDoc := `{"openapi":"3.1.0","paths":{"/next":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
				if err = store.UpsertDeploymentOpenAPIDoc(ctx, deployment.ID, owner, target.ID, []byte(targetDoc), "manual_upload", false); err != nil {
					t.Fatal(err)
				}

				releases := store.(state.ProjectReleaseSetStore)
				publish := func(id string) state.ProjectReleaseSet {
					t.Helper()
					graph, e := releases.PublishProjectReleaseSet(ctx, owner, project.ID, "production", 3600, []state.ProjectReleaseMember{{AppID: target.ID, DeploymentID: id}})
					if e != nil {
						t.Fatal(e)
					}
					return graph
				}
				if scenario != "missing_graph" {
					publish(deployment.ID)
				}
				extra := state.Deployment{}
				if scenario == "graph_zero_weight" || scenario == "promoted" || scenario == "rolled_back" || scenario == "wrong_member" {
					extra, err = store.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: target.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:next"})
					if err != nil {
						t.Fatal(err)
					}
					if err = store.MarkDeploymentLive(ctx, extra.ID); err != nil {
						t.Fatal(err)
					}
					if scenario == "wrong_member" {
						publish(extra.ID)
					}
				}
				if scenario == "frozen_settings" {
					mode := api.ConsumerAuthModeRequired
					if _, err = store.UpdateApp(ctx, target.ID, state.UpdateAppParams{SetConsumerAuthMode: true, ConsumerAuthMode: &mode}); err != nil {
						t.Fatal(err)
					}
				}
				host := "successor.gregale.dev"
				old := `{"openapi":"3.1.0","paths":{"/health":{"get":{"deprecated":true,"x-gregale-deprecated-at":"2026-10-01T00:00:00Z","x-gregale-sunset-at":"2099-01-01T00:00:00Z","x-gregale-successor":"https://canary-gate.gregale.dev/old","responses":{"200":{"description":"ok"}}}}}}`
				proposed := strings.ReplaceAll(old, "https://canary-gate.gregale.dev/old", "https://"+host+"/next")
				for id, doc := range map[string]string{baseline.ID: old, candidate.ID: proposed} {
					if err = store.UpsertDeploymentOpenAPIDoc(ctx, id, acct.ID, app.ID, []byte(doc), "manual_upload", false); err != nil {
						t.Fatal(err)
					}
				}
				gate, err := store.(state.CanaryRouteGateStore).SetCanaryRouteGate(ctx, acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &zero})
				if err != nil {
					t.Fatal(err)
				}
				check, err := store.(state.RouteRequirementsStore).CheckRouteRequirements(ctx, acct.ID, app.ID, api.CheckRouteRequirementsRequest{DeploymentID: candidate.ID}, testSavedRequirementsChecker(candidate.ID))
				if err != nil {
					t.Fatal(err)
				}
				_, bm, _ := store.GetDeploymentOpenAPIDoc(ctx, baseline.ID, acct.ID)
				_, cm, _ := store.GetDeploymentOpenAPIDoc(ctx, candidate.ID, acct.ID)
				_, tm, _ := store.GetDeploymentOpenAPIDoc(ctx, deployment.ID, owner)
				mapping := api.RouteLifecycleMapping{Method: "get", Path: "/health", SuccessorMethod: "get", SuccessorPath: "/next", SuccessorURL: "https://" + host + "/next", SuccessorAppID: target.ID, SuccessorDeploymentID: deployment.ID, SuccessorContractSHA256: fmt.Sprintf("%x", tm.DocSHA256)}
				request := api.ApproveRouteLifecycleRequest{ExpectedGateRevision: &gate.Revision, ExpectedRequirementsRevision: &saved.Revision, ExpectedRemovalPolicyRevision: &zero, ConfigurationSHA256: check.ConfigurationSHA256, BaselineDeploymentID: baseline.ID, CandidateDeploymentID: candidate.ID, BaselineContractSHA256: fmt.Sprintf("%x", bm.DocSHA256), CandidateContractSHA256: fmt.Sprintf("%x", cm.DocSHA256), Mappings: []api.RouteLifecycleMapping{mapping}}
				validator := func(snapshot state.RoutePolicySnapshot, _, _ *state.RoutePolicyContract, mappings []api.RouteLifecycleMapping) error {
					entry := snapshot.LifecycleSuccessors["GET /health"]
					if entry.Snapshot.App.ID != target.ID || entry.Snapshot.Contract.DeploymentID != deployment.ID {
						t.Fatal("wrong resolved destination")
					}
					if len(entry.Project) == 0 {
						t.Fatal("graph binding missing")
					}
					if scenario == "frozen_settings" && entry.Snapshot.App.ConsumerAuthMode == api.ConsumerAuthModeRequired {
						t.Fatal("mutable app settings used instead of frozen spec")
					}
					return nil
				}
				approvals := store.(state.RouteLifecycleApprovalStore)
				receipt, err := approvals.ApproveRouteLifecycle(ctx, acct.ID, app.ID, "owner:test", request, automaticCheckFingerprint, validator)
				if scenario == "missing_graph" || scenario == "wrong_member" {
					if err == nil {
						t.Fatal("unsafe successor approved")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "promoted", "rolled_back":
					publish(extra.ID)
					if scenario == "rolled_back" {
						publish(deployment.ID)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if err = store.(state.AutomaticRouteCheckStore).QueueAutomaticRouteCheck(ctx, acct.ID, app.ID, candidate.ID); err != nil {
					t.Fatal(err)
				}
				claim, err := claimCandidateRouteCheck(t, store, candidate.ID)
				if err != nil || !finishAutomaticCheck(t, store, claim) {
					t.Fatal(err)
				}
				var decision api.RouteGateDecision
				_, _, err = store.(state.CanaryAdvancer).AdvanceCanary(ctx, candidate.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteCheckFingerprint: automaticCheckFingerprint, RouteGateDecision: &decision, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "successor-test"}})
				page, e := store.(state.RouteLifecycleHistoryStore).ListRouteLifecycleHistory(ctx, acct.ID, app.ID, 1, "")
				if e != nil || len(page.Entries) != 1 || len(page.Entries[0].Approvals) != 1 || len(page.Entries[0].Approvals[0].GraphIDs) != 1 {
					t.Fatalf("graph history %+v %v", page, e)
				}
				if scenario == "graph" || scenario == "graph_zero_weight" || scenario == "frozen_settings" {
					if err != nil || len(decision.LifecycleApprovalIDs) != 1 || decision.LifecycleApprovalIDs[0] != receipt.ID {
						t.Fatalf("valid %+v %v", decision, err)
					}
				} else {
					var blocked *state.RouteGateBlockedError
					if !errors.As(err, &blocked) {
						t.Fatalf("stale successor allowed %+v %v", decision, err)
					}
					persisted, e := approvals.GetRouteLifecycleApproval(ctx, acct.ID, app.ID, receipt.ID)
					if e != nil || persisted.InvalidatedAt == nil {
						t.Fatalf("receipt not permanently invalidated %+v %v", persisted, e)
					}
				}
			})
		}
	}
}
