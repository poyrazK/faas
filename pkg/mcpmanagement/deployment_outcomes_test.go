// adr: 20261024234500
package mcpmanagement

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func deploymentOutcomePlan(t *testing.T, m *management, mode string, source bool) string {
	t.Helper()
	var value any
	var err error
	if source {
		value, err = m.planSourceDeploy(t.Context(), planSourceDeployArgs{App: "alpha", Repo: "team/repo", Ref: "main", Reason: "PRIVATE_DEPLOY_REASON", RolloutMode: mode})
	} else if mode != "" {
		value, err = m.planWorkloadRollout(t.Context(), planWorkloadRolloutArgs{App: "alpha", Image: testImage, Reason: "PRIVATE_DEPLOY_REASON"})
	} else {
		value, err = m.planDeploy(t.Context(), planDeployArgs{App: "alpha", Image: testImage, Reason: "PRIVATE_DEPLOY_REASON"})
	}
	if err != nil {
		t.Fatal(err)
	}
	return value.(releasePlan).ID
}
func applySubmission(m *management, ctx context.Context, id, mode string) (any, error) {
	if mode != "" {
		return m.applyWorkloadRollout(ctx, applyArgs{PlanID: id})
	}
	return m.applyDeploy(ctx, applyArgs{PlanID: id})
}

func TestDeploymentOutcomeLostReplyExpiredRestart(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		for _, mode := range []string{"", api.ExecutionModeService, api.ExecutionModeWorker} {
			for _, source := range []bool{false, true} {
				label := protocol + "/" + mode + map[bool]string{false: "/image", true: "/source"}[source]
				t.Run(label, func(t *testing.T) {
					f, _, _ := newReleaseFixture(t, true)
					f.app.Manifest.ExecutionMode = mode
					store, path, key := testStateStore(t)
					var id string
					// Inspect the actual durable journal at the native write boundary.
					backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method == http.MethodPost && (r.URL.Path == "/v1/apps/alpha/deployments" || r.URL.Path == "/v1/apps/alpha/deployments/source-ref") {
							var refs deploymentOutcomeSnapshot
							var plans map[string]storedPlan
							if err := store.read(deploymentOutcomeNamespace+"submission", &refs); err != nil {
								t.Error(err)
							}
							if err := store.read("plans/submission", &plans); err != nil {
								t.Error(err)
							}
							if refs.Items[id].AttemptID != r.Header.Get(api.DeploymentAttemptHeader) || !plans[id].Attempted || !validDeploymentOutcomeLookup(refs.Items[id]) {
								t.Error("native write preceded atomic attempt persistence")
							}
						}
						f.serveHTTP(w, r)
					}))
					t.Cleanup(backend.Close)
					opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "submission"}
					_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
					if err != nil {
						t.Fatal(err)
					}
					id = deploymentOutcomePlan(t, m, mode, source)
					f.mu.Lock()
					f.ambiguous = true
					f.mu.Unlock()
					if _, err := applySubmission(m, t.Context(), id, mode); err == nil {
						t.Fatal("lost reply succeeded")
					}
					ref := m.deploymentOutcomes[id]
					if len(f.attempts) != 1 || f.attempts[0] != ref.AttemptID {
						t.Fatal("missing native attempt identity")
					}
					ciphertext, _ := os.ReadFile(path)
					if bytes.Contains(ciphertext, []byte(ref.AttemptID)) || bytes.Contains(ciphertext, []byte(ref.ArtifactHash)) {
						t.Fatal("lookup was not encrypted")
					}
					// Retrying after the HTTP response cache disappears still reuses the native receipt.
					f.mu.Lock()
					f.keys = map[string]api.DeploymentResponse{}
					f.mu.Unlock()
					if _, err := applySubmission(m, t.Context(), id, mode); err != nil {
						t.Fatal(err)
					}
					if f.writes != 1 || len(f.attempts) != 2 || f.attempts[1] != ref.AttemptID {
						t.Fatal("retry changed durable attempt or created a new deployment")
					}
					// Retain uncertainty for the expired read scenario despite recovering the live retry.
					m.plans[id].result = nil
					m.plans[id].ExpiresAt = time.Now().Add(-time.Minute)
					if err := m.persistPlans(); err != nil {
						t.Fatal(err)
					}
					if err := store.Close(); err != nil {
						t.Fatal(err)
					}
					next, err := OpenStateStore(path, key)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = next.Close() })
					opts.planStore, opts.AllowWrites = next, false
					_, restored, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
					if err != nil {
						t.Fatal(err)
					}
					if len(restored.plans) != 0 || restored.deploymentOutcomes[id].AttemptID != ref.AttemptID {
						t.Fatal("expired payload was restored or reference changed")
					}
					cs, ctx := connectConfiguredTestServer(t, backend, opts, protocol)
					selector := getActionArgs{App: "alpha", PlanID: id}
					before, _ := os.ReadFile(path)
					history := callTool(t, ctx, cs, "gregale_get_action", selector, false)
					var action actionView
					if json.Unmarshal(history["action"], &action) != nil || action.Apply.State != "outcome_unknown" || action.Recovery == nil || action.Recovery.Outcome == nil || action.Recovery.Check.Tool != "" || action.PlanAvailable {
						t.Fatalf("expired discovery=%s", history)
					}
					resource, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: action.Recovery.ResourceURI})
					if err != nil {
						t.Fatal(err)
					}
					recipe := recoveryResourceView(t, resource, action.Recovery.ResourceURI)
					if recipe.Workflow.Review.Tool != "" || recipe.Workflow.Check.Tool != "" || recipe.Workflow.Outcome == nil || f.submissionReads != 0 {
						t.Fatal("recipe restored write authority or read native receipt")
					}
					// Mutable deployment state can advance without invalidating acceptance evidence.
					f.mu.Lock()
					d := f.deployments["release-new"]
					d.Status = "superseded"
					d.RolloutState = "complete"
					d.TrafficPercent = 0
					f.deployments[d.ID] = d
					f.mu.Unlock()
					out := callTool(t, ctx, cs, recipe.Workflow.Outcome.Tool, recipe.Workflow.Outcome.Arguments, false)
					var receipt actionReceipt
					var observation savedPlanReference
					if string(out["outcome"]) != `"verified_receipt"` || string(out["phase"]) != `"apply"` || string(out["native_completion"]) != `"not_observed"` || json.Unmarshal(out["receipt"], &receipt) != nil || receipt.DeploymentID != "release-new" || json.Unmarshal(out["observation"], &observation) != nil {
						t.Fatalf("acceptance=%s", out)
					}
					expected := "gregale_get_deployment"
					if mode != "" {
						expected = "gregale_get_rollout"
					}
					if observation.Tool != expected {
						t.Fatal("wrong observation tool")
					}
					callTool(t, ctx, cs, observation.Tool, observation.Arguments, false)
					after, _ := os.ReadFile(path)
					if !bytes.Equal(before, after) || f.writes != 1 {
						t.Fatal("read-only recovery mutated state")
					}
					raw, _ := json.Marshal(out)
					for _, private := range []string{ref.AttemptID, ref.ArtifactHash, "PRIVATE_DEPLOY_REASON", testImage, `"attempt_id"`, `"source"`, `"expected_review"`} {
						if bytes.Contains(raw, []byte(private)) {
							t.Fatalf("outcome leaked %s", private)
						}
					}
				})
			}
		}
	}
}

func TestNamedDeploymentOutcome(t *testing.T) {
	for _, mode := range []string{"", api.ExecutionModeService, api.ExecutionModeWorker} {
		t.Run(mode, func(t *testing.T) {
			f, b, opts := namedStageOptions(t)
			f.mode = mode
			_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if mode == "" {
				value, err = m.planDeploy(t.Context(), planDeployArgs{App: "alpha", Project: "shop", Environment: "staging", Image: testImage, Reason: "named submission"})
			} else {
				value, err = m.planWorkloadRollout(t.Context(), planWorkloadRolloutArgs{App: "alpha", Project: "shop", Environment: "staging", Image: testImage, Reason: "named submission"})
			}
			if err != nil {
				t.Fatal(err)
			}
			id := value.(releasePlan).ID
			f.ambiguous = true
			if _, err := applySubmission(m, t.Context(), id, mode); err == nil {
				t.Fatal("lost reply succeeded")
			}
			m.plans[id].ExpiresAt = time.Now().Add(-time.Minute)
			if err := m.persistPlans(); err != nil {
				t.Fatal(err)
			}
			out, err := m.getActionOutcome(t.Context(), budgetHistorySelector(id))
			if err != nil {
				t.Fatal(err)
			}
			guard := out.(*guardedRead)
			if err := guard.check(t.Context()); err != nil {
				t.Fatal(err)
			}
			if guard.value.(actionOutcome).Outcome != "verified_receipt" || len(f.attempts) != 1 || !api.ValidDeploymentAttemptID(f.attempts[0]) || f.writes != 1 {
				t.Fatal("named acceptance lost identity")
			}
			f.mu.Lock()
			f.envID = "replacement"
			f.mu.Unlock()
			if guard.check(t.Context()) == nil {
				t.Fatal("recreated environment inherited acceptance")
			}
		})
	}
}

func TestDeploymentOutcomeLegacyAttemptsAndPruning(t *testing.T) {
	f, _, _ := newReleaseFixture(t, true)
	b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(b.Close)
	store, _, _ := testStateStore(t)
	opts := Options{APIBase: b.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "legacy"}
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	id := deploymentOutcomePlan(t, m, "", false)
	m.plans[id].attempted = true // Old bridge recorded the attempt without native identity.
	if err := m.persistPlans(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.applyDeploy(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	if len(m.deploymentOutcomes) != 0 || len(f.attempts) != 1 || f.attempts[0] != "" {
		t.Fatal("legacy attempt was assigned a new native identity")
	}
	out, err := m.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
	if err != nil {
		t.Fatal(err)
	}
	if out.(*guardedRead).value.(actionOutcome).Reason != "lookup_unavailable" || f.submissionReads != 0 {
		t.Fatal("legacy history fabricated lookup evidence")
	}
	id2 := deploymentOutcomePlan(t, m, "", false)
	if _, err := m.applyDeploy(t.Context(), applyArgs{PlanID: id2}); err != nil {
		t.Fatal(err)
	}
	ref := m.deploymentOutcomes[id2]
	delete(m.plans, id2)
	entry := m.actions[id2]
	entry.RetainedUntil = time.Now().Add(-time.Second)
	m.actions[id2] = entry
	if err := m.persistPlans(); err != nil || len(m.deploymentOutcomes) != 0 {
		t.Fatal("pruning retained private attempt")
	}
	if err := store.updateMany(map[string]any{deploymentOutcomeNamespace + "revoked": deploymentOutcomeSnapshot{Version: 1, Items: map[string]deploymentOutcomeLookup{id2: ref}}, deploymentOutcomeNamespace + "active": deploymentOutcomeSnapshot{Version: 1, Items: map[string]deploymentOutcomeLookup{id2: ref}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{"active": {}}}); err != nil {
		t.Fatal(err)
	}
	var revoked, active deploymentOutcomeSnapshot
	if err := store.read(deploymentOutcomeNamespace+"revoked", &revoked); err != nil || len(revoked.Items) != 0 {
		t.Fatal("revocation retained lookup")
	}
	if err := store.read(deploymentOutcomeNamespace+"active", &active); err != nil || !reflect.DeepEqual(active.Items[id2], ref) {
		t.Fatal("revocation deleted another grant")
	}
}

func TestDeploymentOutcomeMismatchedEvidence(t *testing.T) {
	for _, change := range []string{"attempt", "app", "receipt_missing", "receipt_deployment", "accepted_at", "deployment_missing", "scope", "artifact", "kind", "source_commit", "source_build"} {
		t.Run(change, func(t *testing.T) {
			f, _, _ := newReleaseFixture(t, true)
			b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			t.Cleanup(b.Close)
			_, m, err := newManagementServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL, Token: testToken, AllowWrites: true})
			if err != nil {
				t.Fatal(err)
			}
			id := deploymentOutcomePlan(t, m, "", strings.HasPrefix(change, "source_"))
			f.ambiguous = true
			if _, err := m.applyDeploy(t.Context(), applyArgs{PlanID: id}); err == nil {
				t.Fatal("lost reply succeeded")
			}
			attempt := m.deploymentOutcomes[id].AttemptID
			f.mu.Lock()
			receipt := f.submissions[attempt]
			d := f.deployments[receipt.DeploymentID]
			switch change {
			case "attempt":
				receipt.AttemptID = strings.Repeat("0", 32)
			case "app":
				receipt.AppID = "different-app"
			case "receipt_deployment":
				receipt.DeploymentID = "../invalid"
			case "accepted_at":
				receipt.AcceptedAt = time.Time{}
			case "scope":
				d.Scope = "production"
			case "artifact":
				d.ImageDigest = "different/image@sha256:" + strings.Repeat("b", 64)
			case "kind":
				d.Kind = "github"
			case "source_commit":
				d.CommitSHA = strings.Repeat("b", 40)
			case "source_build":
				d.BuildID = ""
			}
			f.submissions[attempt] = receipt
			f.deployments[d.ID] = d
			if change == "receipt_missing" {
				delete(f.submissions, attempt)
			}
			if change == "deployment_missing" {
				delete(f.deployments, d.ID)
			}
			f.mu.Unlock()
			value, err := m.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
			if err != nil {
				t.Fatal(err)
			}
			guard := value.(*guardedRead)
			if err := guard.check(t.Context()); err != nil {
				t.Fatal(err)
			}
			out := guard.value.(actionOutcome)
			if out.Outcome != "unresolved" || out.Receipt != nil || out.Observation != nil || f.writes != 1 {
				t.Fatalf("invalid evidence accepted: %+v", out)
			}
		})
	}
}

func TestDeploymentOutcomeFinalGuards(t *testing.T) {
	for _, change := range []string{"reference", "removed", "history", "retention", "binding", "account", "consent", "storage"} {
		t.Run(change, func(t *testing.T) {
			f, _, _ := newReleaseFixture(t, true)
			b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			t.Cleanup(b.Close)
			store, _, _ := testStateStore(t)
			_, m, err := newManagementServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL, Token: testToken, AllowWrites: true, AllowedApps: map[string]string{"alpha": "app-one"}, planStore: store, planNamespace: "guard"})
			if err != nil {
				t.Fatal(err)
			}
			id := deploymentOutcomePlan(t, m, "", false)
			f.ambiguous = true
			if _, err := m.applyDeploy(t.Context(), applyArgs{PlanID: id}); err == nil {
				t.Fatal("lost reply succeeded")
			}
			value, err := m.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
			if err != nil {
				t.Fatal(err)
			}
			guard := value.(*guardedRead)
			switch change {
			case "reference":
				ref := m.deploymentOutcomes[id]
				ref.AttemptID = strings.Repeat("0", 32)
				m.deploymentOutcomes[id] = ref
			case "removed":
				delete(m.deploymentOutcomes, id)
			case "history":
				delete(m.actions, id)
			case "retention":
				entry := m.actions[id]
				entry.RetainedUntil = time.Now().Add(-time.Minute)
				m.actions[id] = entry
			case "binding":
				entry := m.actions[id]
				entry.AppID = "replaced"
				m.actions[id] = entry
			case "account":
				f.mu.Lock()
				f.account = "different-account"
				f.mu.Unlock()
			case "consent":
				m.opts.AllowedApps["alpha"] = "replaced"
			case "storage":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if guard.check(t.Context()) == nil {
				t.Fatal("final guard released changed acceptance reference")
			}
			if f.writes != 1 {
				t.Fatal("outcome replayed a native write")
			}
		})
	}
}

func TestDeploymentOutcomeSnapshotValidationAndLegacyPreservation(t *testing.T) {
	for _, change := range []string{"future", "attempt", "binding", "artifact", "orphan", "unknown_namespace", "stale_history"} {
		t.Run(change, func(t *testing.T) {
			f, _, _ := newReleaseFixture(t, true)
			b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			t.Cleanup(b.Close)
			store, _, _ := testStateStore(t)
			opts := Options{APIBase: b.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "compat"}
			client := api.NewClient(b.URL, testToken)
			_, m, err := newManagementServer(client, opts)
			if err != nil {
				t.Fatal(err)
			}
			id := deploymentOutcomePlan(t, m, "", false)
			var initial actionHistorySnapshot
			if err := store.read("actions/compat", &initial); err != nil {
				t.Fatal(err)
			}
			if _, err := m.applyDeploy(t.Context(), applyArgs{PlanID: id}); err != nil {
				t.Fatal(err)
			}
			var saved deploymentOutcomeSnapshot
			if err := store.read(deploymentOutcomeNamespace+"compat", &saved); err != nil {
				t.Fatal(err)
			}
			ref := saved.Items[id]
			switch change {
			case "future":
				saved.Version = 2
			case "attempt":
				ref.AttemptID = "bad"
				saved.Items[id] = ref
			case "binding":
				ref.Binding = strings.Repeat("0", 64)
				saved.Items[id] = ref
			case "artifact":
				ref.ArtifactHash = strings.Repeat("0", 64)
				saved.Items[id] = ref
			case "orphan":
				saved.Items[strings.Repeat("f", 64)] = ref
				delete(saved.Items, id)
			case "stale_history":
				if err := store.update("actions/compat", initial); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.updateMany(map[string]any{deploymentOutcomeNamespace + "compat": saved, "future/opaque": map[string]int{"version": 9}}); err != nil {
				t.Fatal(err)
			}
			_, restored, err := newManagementServer(client, opts)
			valid := change == "orphan" || change == "unknown_namespace" || change == "stale_history"
			if valid != (err == nil) {
				t.Fatalf("restore %s: %v", change, err)
			}
			if valid {
				if change == "orphan" && len(restored.deploymentOutcomes) != 0 {
					t.Fatal("orphan was retained or legacy identity minted")
				}
				if change == "stale_history" && restored.actions[id].Apply.State != "accepted" {
					t.Fatal("live N-1 receipt failed history reconciliation")
				}
				var unknown map[string]int
				if err := store.read("future/opaque", &unknown); err != nil || unknown["version"] != 9 {
					t.Fatal("unknown namespace changed")
				}
			}
		})
	}
}
