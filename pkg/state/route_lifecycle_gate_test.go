package state_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCanaryLifecycleDeclarations(t *testing.T) {
	for _, backend := range []string{"mem", "pg"} {
		for _, scenario := range []string{"removed_metadata", "earlier_sunset", "changed_successor", "premature_removal", "report", "unchanged", "missing_baseline", "staging_isolated", "baseline_changed"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				var store state.Store = state.NewMemStore()
				if backend == "pg" {
					store = routePolicyPgStore(t)
				}
				acct, app, stable, candidate := routeGateFixture(t, store)
				saveGateIntent(t, store, acct, app)
				now := time.Now().UTC().Truncate(time.Second)
				deprecation := now.Add(-24 * time.Hour).Format(time.RFC3339)
				sunset := now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
				baseline := fmt.Sprintf(`{"openapi":"3.1.0","paths":{"/health":{"get":{"deprecated":true,"x-gregale-deprecated-at":%q,"x-gregale-sunset-at":%q,"x-gregale-successor":"https://example.com/new"}}}}`, deprecation, sunset)
				proposed := baseline
				switch scenario {
				case "removed_metadata", "report", "baseline_changed":
					proposed = gateContract
				case "earlier_sunset":
					proposed = strings.ReplaceAll(proposed, sunset, now.Add(24*time.Hour).Format(time.RFC3339))
				case "changed_successor":
					proposed = strings.ReplaceAll(proposed, "https://example.com/new", "https://example.com/other")
				case "premature_removal":
					proposed = `{"openapi":"3.1.0","paths":{}}`
				}
				baselineToStore := baseline
				if scenario == "baseline_changed" {
					baselineToStore = gateContract
				}
				if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), stable.ID, acct.ID, app.ID, []byte(baselineToStore), "manual_upload", false); err != nil {
					t.Fatal(err)
				}
				if scenario == "missing_baseline" {
					if err := store.DeleteDeploymentOpenAPIDoc(t.Context(), stable.ID, acct.ID); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "staging_isolated" {
					// An unrelated environment's declarations do not protect production routes.
					extra, err := store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:staging", Scope: "staging"})
					if err != nil {
						t.Fatal(err)
					}
					if err := store.MarkDeploymentLive(t.Context(), extra.ID); err != nil {
						t.Fatal(err)
					}
					if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), extra.ID, acct.ID, app.ID, []byte(strings.ReplaceAll(baseline, "/health", "/unrelated")), "manual_upload", false); err != nil {
						t.Fatal(err)
					}
				}
				if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), candidate.ID, acct.ID, app.ID, []byte(proposed), "manual_upload", false); err != nil {
					t.Fatal(err)
				}
				if scenario != "report" {
					zero := int64(0)
					if _, err := store.(state.CanaryRouteGateStore).SetCanaryRouteGate(t.Context(), acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
						t.Fatal(err)
					}
				}
				queue := store.(state.AutomaticRouteCheckStore)
				if err := queue.QueueAutomaticRouteCheck(t.Context(), acct.ID, app.ID, candidate.ID); err != nil {
					t.Fatal(err)
				}
				for {
					claim, err := queue.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
					if err != nil {
						t.Fatal(err)
					}
					if claim.DeploymentID == "" {
						t.Fatal("candidate route check missing")
					}
					if !finishAutomaticCheck(t, store, claim) {
						t.Fatal("route check completion")
					}
					if claim.DeploymentID == candidate.ID {
						break
					}
				}
				if scenario == "baseline_changed" {
					if err := store.UpsertDeploymentOpenAPIDoc(t.Context(), stable.ID, acct.ID, app.ID, []byte(baseline), "manual_upload", false); err != nil {
						t.Fatal(err)
					}
				}
				var decision api.RouteGateDecision
				_, _, err := store.(state.CanaryAdvancer).AdvanceCanary(t.Context(), candidate.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteCheckFingerprint: automaticCheckFingerprint, RouteGateDecision: &decision, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "lifecycle-test"}})
				allowed := scenario == "unchanged" || scenario == "report" || scenario == "staging_isolated"
				if allowed {
					if err != nil {
						t.Fatalf("allowed %+v %v", decision, err)
					}
				} else {
					var blocked *state.RouteGateBlockedError
					if !errors.As(err, &blocked) {
						t.Fatalf("failed open %+v %v", decision, err)
					}
					fresh, _ := store.DeploymentByID(t.Context(), candidate.ID)
					if fresh.CanaryStep != 0 || fresh.TrafficPercent != 1 {
						t.Fatal("blocked lifecycle changed traffic")
					}
				}
				if !allowed || scenario == "report" {
					found := false
					for _, reason := range decision.Reasons {
						if strings.HasPrefix(reason, "lifecycle_") {
							found = true
						}
					}
					if !found {
						t.Fatalf("lifecycle finding missing %+v", decision)
					}
				}
			})
		}
	}
}
