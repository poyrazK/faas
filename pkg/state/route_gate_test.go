package state_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const gateContract = `{"openapi":"3.1.0","paths":{"/health":{"get":{}}}}`

func routeGateFixture(t *testing.T, store state.Store) (state.Account, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	acct, err := store.CreateAccount(t.Context(), "canary-gate@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "canary-gate", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	stable, err := store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), Kind: state.DeploymentKindImage, AppID: app.ID, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(t.Context(), stable.ID); err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), Kind: state.DeploymentKindImage, AppID: app.ID, ImageDigest: "sha256:candidate", CanaryPreset: "balanced", CanaryStep: 0, CanaryTotalSteps: 4, RolloutState: "pending", TrafficPercent: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(t.Context(), candidate.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), stable.ID, acct.ID, app.ID, []byte(gateContract), "manual_upload", false); err != nil {
		t.Fatal(err)
	}
	return acct, app, stable, candidate
}

func saveGateIntent(t *testing.T, store state.Store, acct state.Account, app state.App) api.SavedRouteRequirements {
	t.Helper()
	zero := int64(0)
	saved, err := store.(state.RouteRequirementsStore).SaveRouteRequirements(t.Context(), acct.ID, app.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &zero, Requirements: api.RouteRequirementsConfig{Version: 2, Public: []api.RoutePublicException{{Method: "GET", Path: "/health", Reason: "private rationale"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestCanaryRouteGateEvidenceAndRecovery(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		for _, scenario := range []string{"missing", "pending", "running", "retrying", "violated", "unknown", "truncated", "capture_changed", "intent_changed", "policy_changed", "no_fingerprint", "report", "satisfied"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				if backend == "pg" {
					store = routePolicyPgStore(t)
				}
				acct, app, stable, candidate := routeGateFixture(t, store)
				saved := saveGateIntent(t, store, acct, app)
				gateStore := store.(state.CanaryRouteGateStore)
				gate, err := gateStore.GetCanaryRouteGate(t.Context(), acct.ID, app.ID)
				if err != nil || gate.Mode != "report" || gate.Revision != 0 {
					t.Fatalf("default: %+v %v", gate, err)
				}
				if scenario != "report" {
					gate, err = gateStore.SetCanaryRouteGate(t.Context(), acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &gate.Revision})
					if err != nil {
						t.Fatal(err)
					}
				}
				queue := store.(state.AutomaticRouteCheckStore)
				if scenario != "missing" && scenario != "unknown" {
					doc := gateContract
					if scenario == "violated" || scenario == "report" {
						doc = `{"openapi":"3.1.0","paths":{"/health":{"get":{}},"/admin":{"post":{}}}}`
					}
					if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), candidate.ID, acct.ID, app.ID, []byte(doc), "manual_upload", scenario == "truncated"); err != nil {
						t.Fatal(err)
					}
				} else if scenario == "unknown" {
					if err := queue.QueueAutomaticRouteCheck(t.Context(), acct.ID, app.ID, candidate.ID); err != nil {
						t.Fatal(err)
					}
				}
				if scenario != "missing" && scenario != "pending" {
					claim, err := claimCandidateRouteCheck(t, store, candidate.ID)
					if err != nil {
						t.Fatal(err)
					}
					if scenario == "retrying" {
						if _, err := queue.FailAutomaticRouteCheck(t.Context(), claim); err != nil {
							t.Fatal(err)
						}
					} else if scenario != "running" && !finishAutomaticCheck(t, store, claim) {
						t.Fatal("check completion")
					}
				}
				switch scenario {
				case "capture_changed":
					if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), candidate.ID, acct.ID, app.ID, []byte(`{"openapi":"3.1.0","paths":{"/health":{"get":{}},"/extra":{"post":{}}}}`), "manual_upload", false); err != nil {
						t.Fatal(err)
					}
				case "intent_changed":
					saved.Requirements.Public = append(saved.Requirements.Public, api.RoutePublicException{Method: "GET", Path: "/extra", Reason: "public"})
					if _, err := store.(state.RouteRequirementsStore).SaveRouteRequirements(t.Context(), acct.ID, app.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &saved.Revision, Requirements: saved.Requirements}); err != nil {
						t.Fatal(err)
					}
				case "policy_changed":
					mode := api.ConsumerAuthModeRequired
					if _, err := store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetConsumerAuthMode: true, ConsumerAuthMode: &mode}); err != nil {
						t.Fatal(err)
					}
				}
				var decision api.RouteGateDecision
				params := state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteCheckFingerprint: automaticCheckFingerprint, RouteGateDecision: &decision, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "gate-test"}}
				if scenario == "no_fingerprint" {
					params.RouteCheckFingerprint = nil
				}
				updated, auditID, err := store.(state.CanaryAdvancer).AdvanceCanary(t.Context(), candidate.ID, params)
				if scenario == "satisfied" || scenario == "report" {
					if err != nil || updated.TrafficPercent != 10 || auditID == 0 || decision.Status == "blocked" {
						t.Fatalf("allowed: %+v %+v %v", updated, decision, err)
					}
					audits, err := store.ListDeploymentAudit(t.Context(), candidate.ID, 10)
					if err != nil || len(audits) != 1 || !strings.Contains(string(audits[0].Data), `"route_gate"`) || strings.Contains(string(audits[0].Data), "private rationale") {
						t.Fatal("gate audit missing or private context leaked")
					}
					return
				}
				var blocked *state.RouteGateBlockedError
				if !errors.As(err, &blocked) || decision.Status != "blocked" || len(decision.Reasons) == 0 {
					t.Fatalf("failed open: %+v %v", decision, err)
				}
				current, _ := store.DeploymentByID(t.Context(), candidate.ID)
				old, _ := store.DeploymentByID(t.Context(), stable.ID)
				if current.CanaryStep != 0 || current.TrafficPercent != 1 || old.TrafficPercent != 99 {
					t.Fatal("blocked advance changed traffic")
				}
				audits, err := store.ListDeploymentAudit(t.Context(), candidate.ID, 10)
				if err != nil || len(audits) != 0 {
					t.Fatal("blocked advance wrote a traffic audit")
				}
				if scenario == "missing" || scenario == "policy_changed" {
					if !decision.CheckQueued {
						t.Fatal("fresh evidence was not durably requested")
					}
					claim, err := claimCandidateRouteCheck(t, store, candidate.ID)
					if err != nil || claim.DeploymentID != candidate.ID {
						t.Fatalf("refresh handoff lost: %+v %v", claim, err)
					}
					if !finishAutomaticCheck(t, store, claim) {
						t.Fatal("refreshed check could not complete")
					}
					if scenario == "policy_changed" {
						if _, _, err := store.(state.CanaryAdvancer).AdvanceCanary(t.Context(), candidate.ID, params); err != nil {
							t.Fatalf("fresh pass blocked: %v", err)
						}
						return
					}
				}
				for _, action := range []string{"advance", "promote"} {
					if _, _, err := store.RecoverRollout(t.Context(), app.ID, action, "legacy"); !errors.As(err, &blocked) {
						t.Fatalf("legacy bypass %s: %v", action, err)
					}
				}
				if _, _, err := store.RecoverRollout(t.Context(), app.ID, "abort", "route gate"); err != nil {
					t.Fatalf("abort blocked: %v", err)
				}
				old, _ = store.DeploymentByID(t.Context(), stable.ID)
				if old.TrafficPercent != 100 {
					t.Fatal("abort did not restore stable traffic")
				}
			})
		}
	}
}

func TestCanaryRouteGateConfiguration(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "pg" {
				store = routePolicyPgStore(t)
			}
			acct, app, _, _ := routeGateFixture(t, store)
			gates := store.(state.CanaryRouteGateStore)
			zero := int64(0)
			request := api.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &zero}
			if _, err := gates.SetCanaryRouteGate(t.Context(), acct.ID, app.ID, request); !errors.Is(err, state.ErrRouteGateRequirements) {
				t.Fatalf("missing intent: %v", err)
			}
			saveGateIntent(t, store, acct, app)
			if _, err := gates.SetCanaryRouteGate(t.Context(), acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "enforce"}); err == nil {
				t.Fatal("missing revision")
			}
			gate, err := gates.SetCanaryRouteGate(t.Context(), acct.ID, app.ID, request)
			if err != nil || gate.Revision != 1 {
				t.Fatalf("enable: %+v %v", gate, err)
			}
			if _, err := gates.SetCanaryRouteGate(t.Context(), acct.ID, app.ID, request); !errors.Is(err, state.ErrRouteGateRevision) {
				t.Fatal("stale writer changed gate")
			}
			changedTime := gate.UpdatedAt
			gate.UpdatedAt = new(time.Time)
			unchanged, err := gates.GetCanaryRouteGate(t.Context(), acct.ID, app.ID)
			if err != nil || !unchanged.UpdatedAt.Equal(*changedTime) {
				t.Fatal("caller mutated gate storage")
			}
			other, _ := store.CreateAccount(t.Context(), "foreign-gate@example.test", api.PlanPro)
			if _, err := gates.GetCanaryRouteGate(t.Context(), other.ID, app.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("foreign read")
			}
			if _, err := gates.SetCanaryRouteGate(t.Context(), other.ID, app.ID, request); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("foreign write")
			}
			if err := store.UpdateAccountPlan(t.Context(), acct.ID, api.PlanFree); err != nil {
				t.Fatal(err)
			}
			one := int64(1)
			if _, err := gates.SetCanaryRouteGate(t.Context(), acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &one}); !errors.Is(err, state.ErrRouteGatePlan) {
				t.Fatal("downgraded plan could enable gate")
			}
			gate, err = gates.SetCanaryRouteGate(t.Context(), acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "report", ExpectedRevision: &one})
			if err != nil || gate.Mode != "report" || gate.Revision != 2 {
				t.Fatalf("disable after downgrade: %+v %v", gate, err)
			}
		})
	}
}

func TestCanaryRouteGateSerializesConcurrentChanges(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		for _, edit := range []string{"capture", "new_rule", "gate"} {
			t.Run(backend+"/"+edit, func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				if backend == "pg" {
					store = routePolicyPgStore(t)
				}
				acct, app, _, candidate := routeGateFixture(t, store)
				saveGateIntent(t, store, acct, app)
				zero := int64(0)
				gate, err := store.(state.CanaryRouteGateStore).SetCanaryRouteGate(t.Context(), acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &zero})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), candidate.ID, acct.ID, app.ID, []byte(gateContract), "manual_upload", false); err != nil {
					t.Fatal(err)
				}
				claim, err := claimCandidateRouteCheck(t, store, candidate.ID)
				if err != nil || !finishAutomaticCheck(t, store, claim) {
					t.Fatal("initial check")
				}
				entered, release := make(chan struct{}), make(chan struct{})
				var once, enteredOnce sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				t.Cleanup(unblock)
				advanceDone := make(chan error, 1)
				go func() {
					fingerprint := func(snapshot state.RoutePolicySnapshot) string {
						enteredOnce.Do(func() { close(entered) })
						<-release
						return automaticCheckFingerprint(snapshot)
					}
					_, _, err := store.(state.CanaryAdvancer).AdvanceCanary(t.Context(), candidate.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteCheckFingerprint: fingerprint, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "race-test"}})
					advanceDone <- err
				}()
				select {
				case <-entered:
				case err := <-advanceDone:
					t.Fatalf("advance did not read evidence: %v", err)
				case <-time.After(10 * time.Second):
					t.Fatal("advance lock timeout")
				}
				editDone, started := make(chan error, 1), make(chan struct{})
				go func() {
					close(started)
					var err error
					switch edit {
					case "capture":
						err = store.UpsertDeploymentOpenAPIDoc(t.Context(), candidate.ID, acct.ID, app.ID, []byte(`{"openapi":"3.1.0","paths":{"/health":{"get":{}},"/new":{"post":{}}}}`), "manual_upload", false)
					case "new_rule":
						_, err = store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: app.ID, MatchHost: app.Slug + ".gregale.dev", MatchPath: "/health", Kind: state.EdgeRuleKindThrottle, Enabled: true, Action: state.EdgeRuleAction{Throttle: &state.EdgeRuleThrottleAction{RequestsPerSecond: 1, Burst: 1}}})
					case "gate":
						_, err = store.(state.CanaryRouteGateStore).SetCanaryRouteGate(t.Context(), acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "report", ExpectedRevision: &gate.Revision})
					}
					editDone <- err
				}()
				<-started
				select {
				case err := <-editDone:
					t.Fatalf("concurrent input changed inside the advance: %v", err)
				case <-time.After(100 * time.Millisecond):
				}
				unblock()
				select {
				case err := <-advanceDone:
					if err != nil {
						t.Fatalf("serialized advance: %v", err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("advance completion timeout")
				}
				select {
				case err := <-editDone:
					if err != nil {
						t.Fatalf("serialized edit: %v", err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("edit completion timeout")
				}
				_, _, err = store.(state.CanaryAdvancer).AdvanceCanary(t.Context(), candidate.ID, state.CanaryAdvanceParams{ExpectedStep: 1, TrafficPercent: 50, RouteCheckFingerprint: automaticCheckFingerprint, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "race-test"}})
				var blocked *state.RouteGateBlockedError
				if edit == "gate" {
					if err != nil {
						t.Fatalf("intentional disable: %v", err)
					}
				} else if !errors.As(err, &blocked) {
					t.Fatalf("later advance reused pre-edit evidence: %v", err)
				}
			})
		}
	}
}

// Baseline captures also enqueue checks; target the evidence under test.
func claimCandidateRouteCheck(t *testing.T, store state.Store, deploymentID string) (state.AutomaticRouteCheckClaim, error) {
	t.Helper()
	for {
		claim, err := store.(state.AutomaticRouteCheckStore).ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
		if err != nil || claim.DeploymentID == "" || claim.DeploymentID == deploymentID {
			return claim, err
		}
		if !finishAutomaticCheck(t, store, claim) {
			t.Fatal("baseline check completion")
		}
	}
}
