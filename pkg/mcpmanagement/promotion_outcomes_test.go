// adr: 20261025090000
package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPromotionOutcomeLostReplyExpiry(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		for _, source := range []bool{false, true} {
			t.Run(protocol+map[bool]string{false: "/image", true: "/source"}[source], func(t *testing.T) {
				f, _, _ := newReleaseFixture(t, true)
				store, path, key := testStateStore(t)
				var id string
				var receipt atomic.Pointer[api.DeploymentPromotionReceipt]
				var reads atomic.Int32
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/v1/apps/alpha/deployment-promotions/") {
						reads.Add(1)
						saved := receipt.Load()
						if r.Method != "GET" || r.Header.Get(api.DeploymentPromotionAttemptHeader) != "" || r.Header.Get("Idempotency-Key") != "" || saved == nil || !strings.HasSuffix(r.URL.Path, saved.AttemptID) {
							w.WriteHeader(404)
							return
						}
						_ = json.NewEncoder(w).Encode(saved)
						return
					}
					if r.Method == "PATCH" && strings.HasSuffix(r.URL.Path, "/traffic") {
						body, _ := io.ReadAll(r.Body)
						r.Body = io.NopCloser(bytes.NewReader(body))
						var request api.UpdateDeploymentTrafficRequest
						if json.Unmarshal(body, &request) != nil {
							t.Error("invalid request")
						}
						var refs promotionOutcomeSnapshot
						var plans map[string]storedPlan
						if err := store.read(promotionOutcomeNamespace+"promotion", &refs); err != nil {
							t.Error(err)
						}
						if err := store.read("plans/promotion", &plans); err != nil {
							t.Error(err)
						}
						ref := refs.Items[id]
						if !plans[id].PromotionAttempted || !validPromotionOutcomeLookup(ref) || ref.AttemptID != r.Header.Get(api.DeploymentPromotionAttemptHeader) {
							t.Error("native promotion preceded atomic attempt persistence")
						}
						f.serveHTTP(w, r)
						hash, _ := api.DeploymentPromotionRequestHash("release-new", request)
						receipt.Store(&api.DeploymentPromotionReceipt{AttemptID: ref.AttemptID, RequestHash: hash, AppID: "app-one", DeploymentID: "release-new", ExpectedServingDeploymentID: "release-base", Scope: "default", AcceptedAt: time.Now().UTC()})
						return
					}
					f.serveHTTP(w, r)
				}))
				t.Cleanup(backend.Close)
				opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "promotion"}
				_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
				if err != nil {
					t.Fatal(err)
				}
				id = deploymentOutcomePlan(t, m, "", source)
				if _, err = m.applyDeploy(t.Context(), applyArgs{PlanID: id}); err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				ready := f.deployments["release-new"]
				ready.Status = "live"
				f.deployments[ready.ID] = ready
				f.ambiguous = true
				f.mu.Unlock()
				if _, err = m.promoteDeployment(t.Context(), applyArgs{PlanID: id}); err == nil {
					t.Fatal("lost promotion reply succeeded")
				}
				ref := m.promotionOutcomes[id]
				encrypted, _ := os.ReadFile(path)
				for _, secret := range []string{ref.AttemptID, ref.RequestHash, ref.ArtifactHash} {
					if bytes.Contains(encrypted, []byte(secret)) {
						t.Fatal("plaintext promotion lookup")
					}
				}
				f.mu.Lock()
				f.current = "release-later"
				d := f.deployments["release-new"]
				d.TrafficPercent = 0
				d.Status = "superseded"
				f.deployments[d.ID] = d
				f.mu.Unlock()
				m.plans[id].ExpiresAt = time.Now().Add(-time.Minute)
				if err = m.persistPlans(); err != nil {
					t.Fatal(err)
				}
				if err = store.Close(); err != nil {
					t.Fatal(err)
				}
				next, err := OpenStateStore(path, key)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = next.Close() })
				opts.planStore, opts.AllowWrites = next, false
				cs, ctx := connectConfiguredTestServer(t, backend, opts, protocol)
				selector := getActionArgs{App: "alpha", PlanID: id}
				before, _ := os.ReadFile(path)
				result := callTool(t, ctx, cs, "gregale_get_action", selector, false)
				var view actionView
				if json.Unmarshal(result["action"], &view) != nil || view.Promotion == nil || view.Promotion.State != "outcome_unknown" || view.Apply.State != "accepted" || view.PlanAvailable || view.Recovery == nil || view.Recovery.Outcome == nil || view.Recovery.PromotionOutcome == nil {
					t.Fatalf("history=%s", result)
				}
				resource, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: view.Recovery.ResourceURI})
				if err != nil {
					t.Fatal(err)
				}
				recipe := recoveryResourceView(t, resource, view.Recovery.ResourceURI)
				if recipe.Workflow.Review.Tool != "" || recipe.Workflow.Check.Tool != "" || recipe.Workflow.PromotionOutcome == nil || reads.Load() != 0 {
					t.Fatal("recipe granted authority or fetched receipt")
				}
				prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_saved_plan_review", Arguments: map[string]string{"app": "alpha", "plan_id": id}})
				if err != nil || len(prompt.Messages) != 1 || reads.Load() != 0 {
					t.Fatalf("passive prompt: %v", err)
				}
				apply := callTool(t, ctx, cs, "gregale_get_action_outcome", selector, false)
				if string(apply["phase"]) != `"apply"` || string(apply["outcome"]) != `"verified_receipt"` {
					t.Fatalf("apply=%s", apply)
				}
				out := callTool(t, ctx, cs, recipe.Workflow.PromotionOutcome.Tool, recipe.Workflow.PromotionOutcome.Arguments, false)
				var observation savedPlanReference
				var accepted actionReceipt
				if string(out["phase"]) != `"promotion"` || string(out["outcome"]) != `"verified_receipt"` || json.Unmarshal(out["receipt"], &accepted) != nil || accepted.DeploymentID != "release-new" || json.Unmarshal(out["observation"], &observation) != nil || observation.Tool != "gregale_get_deployment" || string(out["native_completion"]) != `"not_observed"` {
					t.Fatalf("promotion=%s", out)
				}
				callTool(t, ctx, cs, observation.Tool, observation.Arguments, false)
				raw, _ := json.Marshal(out)
				for _, private := range []string{ref.AttemptID, ref.RequestHash, ref.ArtifactHash, testImage, "PRIVATE_DEPLOY_REASON", "expected_review", "expected_serving_deployment_id"} {
					if bytes.Contains(raw, []byte(private)) {
						t.Fatalf("leaked %s", private)
					}
				}
				after, _ := os.ReadFile(path)
				if !bytes.Equal(before, after) || f.writes != 2 {
					t.Fatal("read-only phase recovery mutated state")
				}
			})
		}
	}
}

func TestPromotionOutcomeNamedAndFinalGuards(t *testing.T) {
	for _, change := range []string{"none", "missing", "hash", "candidate", "serving", "environment", "app", "scope", "project", "accepted_at", "artifact", "final_reference", "final_retention", "final_identity", "final_consent", "final_storage", "only_promotion"} {
		t.Run(change, func(t *testing.T) {
			f, _, opts := namedStageOptions(t)
			store, _, _ := testStateStore(t)
			opts.planStore, opts.planNamespace = store, "named-promotion"
			var receipt *api.DeploymentPromotionReceipt
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/deployment-promotions/") {
					if receipt == nil {
						w.WriteHeader(404)
					} else {
						_ = json.NewEncoder(w).Encode(receipt)
					}
					return
				}
				f.serve(w, r)
			}))
			t.Cleanup(backend.Close)
			opts.APIBase = backend.URL
			_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			value, err := m.planDeploy(t.Context(), planDeployArgs{App: "alpha", Project: "shop", Environment: "staging", Image: testImage, Reason: "promote named"})
			if err != nil {
				t.Fatal(err)
			}
			id := value.(releasePlan).ID
			if _, err = m.applyDeploy(t.Context(), applyArgs{PlanID: id}); err != nil {
				t.Fatal(err)
			}
			if _, err = m.promoteDeployment(t.Context(), applyArgs{PlanID: id}); err != nil {
				t.Fatal(err)
			}
			ref := m.promotionOutcomes[id]
			receipt = &api.DeploymentPromotionReceipt{AttemptID: ref.AttemptID, RequestHash: ref.RequestHash, AppID: "app-one", DeploymentID: "stage-new", ExpectedServingDeploymentID: "stage-base", Scope: "staging", ProjectID: "project-one", EnvironmentID: "env-staging", AcceptedAt: time.Now().UTC()}
			switch change {
			case "missing":
				receipt = nil
			case "hash":
				receipt.RequestHash = strings.Repeat("f", 64)
			case "candidate":
				receipt.DeploymentID = "other"
			case "serving":
				receipt.ExpectedServingDeploymentID = "other"
			case "environment":
				receipt.EnvironmentID = "other"
			case "app":
				receipt.AppID = "other"
			case "scope":
				receipt.Scope = "default"
			case "project":
				receipt.ProjectID = "other"
			case "accepted_at":
				receipt.AcceptedAt = time.Time{}
			case "artifact":
				f.mu.Lock()
				for key, d := range f.cache {
					d.ImageDigest = strings.Replace(testImage, "aaaa", "bbbb", 1)
					f.cache[key] = d
				}
				f.mu.Unlock()
			case "only_promotion":
				delete(m.deploymentOutcomes, id)
				m.plans[id].ExpiresAt = time.Now().Add(-time.Minute)
				if err = m.persistPlans(); err != nil {
					t.Fatal(err)
				}
			}
			args := getActionArgs{App: "alpha", Project: "shop", Environment: "staging", PlanID: id}
			if change == "only_promotion" {
				guard, calls, err := m.savedPromptWorkflow(t.Context(), args, false)
				if err != nil || calls.Outcome != nil || calls.PromotionOutcome == nil || calls.Check.Tool != "" || guard.check(t.Context()) != nil {
					t.Fatalf("promotion-only recipe: %+v %v", calls, err)
				}
			}
			value, err = m.getPhasedActionOutcome(t.Context(), actionOutcomeArgs{getActionArgs: args, Phase: "promotion"})
			if err != nil {
				t.Fatal(err)
			}
			guard := value.(*guardedRead)
			out := guard.value.(actionOutcome)
			switch change {
			case "none", "only_promotion":
				if out.Outcome != "verified_receipt" {
					t.Fatalf("out=%+v", out)
				}
			case "final_reference":
				delete(m.promotionOutcomes, id)
			case "final_retention":
				entry := m.actions[id]
				entry.RetainedUntil = time.Now().Add(-time.Second)
				m.actions[id] = entry
			case "final_identity":
				f.mu.Lock()
				f.envID = "recreated"
				f.mu.Unlock()
			case "final_consent":
				m.opts.AllowedApps = map[string]string{"other": "other"}
			case "final_storage":
				if err = store.Close(); err != nil {
					t.Fatal(err)
				}
			default:
				if out.Outcome != "unresolved" || out.Receipt != nil {
					t.Fatalf("unverified outcome=%+v", out)
				}
			}
			err = guard.check(t.Context())
			if strings.HasPrefix(change, "final_") && err == nil {
				t.Fatal("final guard released stale authority")
			}
			if !strings.HasPrefix(change, "final_") && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPromotionOutcomeLegacyAndUnsupported(t *testing.T) {
	f, _, _ := newReleaseFixture(t, true)
	backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	defer backend.Close()
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, Token: testToken, AllowWrites: true})
	if err != nil {
		t.Fatal(err)
	}
	id := deploymentOutcomePlan(t, m, "", false)
	if _, err = m.applyDeploy(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	args := actionOutcomeArgs{getActionArgs: getActionArgs{App: "alpha", PlanID: id}, Phase: "promotion"}
	value, err := m.getPhasedActionOutcome(t.Context(), args)
	if err != nil || value.(*guardedRead).value.(actionOutcome).Reason != "no_recorded_attempt" {
		t.Fatal("unattempted phase became evidence")
	}
	m.plans[id].promotionAttempted = true
	if _, err = m.promoteDeployment(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	if len(m.promotionOutcomes) != 0 {
		t.Fatal("legacy attempt received a new identity")
	}
	value, err = m.getPhasedActionOutcome(t.Context(), args)
	if err != nil || value.(*guardedRead).value.(actionOutcome).Reason != "lookup_unavailable" {
		t.Fatal("legacy lookup invented evidence")
	}
	args.Phase = "other"
	if _, err = m.getPhasedActionOutcome(t.Context(), args); err == nil {
		t.Fatal("invalid phase accepted")
	}
	// Unknown companion namespaces survive old-style journal updates and grant purge stays isolated.
	store, _, _ := testStateStore(t)
	snapshot := promotionOutcomeSnapshot{Version: 1, Items: map[string]promotionOutcomeLookup{}}
	if err = store.updateMany(map[string]any{promotionOutcomeNamespace + "revoked": snapshot, promotionOutcomeNamespace + "other": map[string]int{"future": 9}}); err != nil {
		t.Fatal(err)
	}
	if err = store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{"other": {}}}); err != nil {
		t.Fatal(err)
	}
	var kept map[string]int
	if err = store.read(promotionOutcomeNamespace+"other", &kept); err != nil || !reflect.DeepEqual(kept, map[string]int{"future": 9}) {
		t.Fatal("grant purge touched another namespace")
	}
}

func TestPromotionOutcomeSnapshotIntegrity(t *testing.T) {
	for _, change := range []string{"version", "binding", "attempt", "candidate", "artifact", "orphan", "prune", "n_minus_one"} {
		t.Run(change, func(t *testing.T) {
			f, _, _ := newReleaseFixture(t, true)
			backend := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			defer backend.Close()
			store, _, _ := testStateStore(t)
			opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "snapshot"}
			_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			id := deploymentOutcomePlan(t, m, "", false)
			if _, err = m.applyDeploy(t.Context(), applyArgs{PlanID: id}); err != nil {
				t.Fatal(err)
			}
			if _, err = m.promoteDeployment(t.Context(), applyArgs{PlanID: id}); err != nil {
				t.Fatal(err)
			}
			ref := m.promotionOutcomes[id]
			var snapshot promotionOutcomeSnapshot
			if err = store.read(promotionOutcomeNamespace+"snapshot", &snapshot); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "version":
				snapshot.Version = 9
			case "binding":
				ref.Binding = strings.Repeat("f", 64)
			case "attempt":
				ref.AttemptID = "invalid"
			case "candidate":
				ref.CandidateID = "other"
			case "artifact":
				ref.ArtifactHash = strings.Repeat("f", 64)
			case "orphan":
				delete(snapshot.Items, id)
				id = strings.Repeat("f", 64)
			case "prune":
				p := m.plans[id]
				p.ExpiresAt = time.Now().Add(-api.ManagementMCPActionHistoryRetention - time.Minute)
				entry := m.actions[id]
				entry.PlanExpiresAt = p.ExpiresAt
				entry.RetainedUntil = p.ExpiresAt.Add(api.ManagementMCPActionHistoryRetention)
				m.actions[id] = entry
				if err = m.persistPlans(); err != nil || len(m.promotionOutcomes) != 0 {
					t.Fatal("retention did not prune lookup")
				}
				return
			case "n_minus_one":
				var history actionHistorySnapshot
				if err = store.read("actions/snapshot", &history); err != nil {
					t.Fatal(err)
				}
				entry := history.Items[id]
				entry.Promotion = &actionPhase{State: "not_attempted"}
				history.Items[id] = entry
				if err = store.update("actions/snapshot", history); err != nil {
					t.Fatal(err)
				}
			}
			snapshot.Items[id] = ref
			if err = store.updateMany(map[string]any{promotionOutcomeNamespace + "snapshot": snapshot, "future/opaque": map[string]int{"version": 9}}); err != nil {
				t.Fatal(err)
			}
			_, restored, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
			valid := change == "orphan" || change == "n_minus_one"
			if valid && err != nil || !valid && err == nil {
				t.Fatalf("restore %s=%v", change, err)
			}
			if valid && change == "orphan" && len(restored.promotionOutcomes) != 0 {
				t.Fatal("orphan lookup survived")
			}
			if valid && change == "n_minus_one" && restored.actions[id].Promotion.State != "accepted" {
				t.Fatal("live original plan did not reconcile stale history")
			}
		})
	}
}
