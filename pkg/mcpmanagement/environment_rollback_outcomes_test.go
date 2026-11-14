// adr: 20261105102920
package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestEnvironmentRollbackOutcomeLostReplyExpiry(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f, _ := newEnvironmentFixture(t)
			store, path, key := testStateStore(t)
			var planID string
			var writes, reads atomic.Int32
			var receipt atomic.Pointer[api.EnvironmentRollbackReceipt]
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/rollback-attempts/") {
					reads.Add(1)
					saved := receipt.Load()
					if r.Method != "GET" || saved == nil || !strings.HasSuffix(r.URL.Path, saved.AttemptID) || r.Header.Get(api.EnvironmentRollbackAttemptHeader) != "" || r.Header.Get(api.EnvironmentRollbackHashHeader) != "" || r.Header.Get("Idempotency-Key") != "" {
						w.WriteHeader(404)
						return
					}
					_ = json.NewEncoder(w).Encode(saved)
					return
				}
				if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/rollback") {
					var saved environmentRollbackSnapshot
					var plans map[string]storedEnvironmentPlan
					if err := store.read(environmentRollbackOutcomeNamespace+"environment-receipt", &saved); err != nil {
						t.Error(err)
					}
					if err := store.read("environment-plans/environment-receipt", &plans); err != nil {
						t.Error(err)
					}
					ref := saved.Items[planID]
					p := plans[planID]
					if !p.Attempted || !validEnvironmentRollbackLookup(ref) || r.Header.Get(api.EnvironmentRollbackAttemptHeader) != ref.AttemptID || r.Header.Get(api.EnvironmentRollbackHashHeader) != ref.RequestHash {
						t.Error("POST preceded original attempt/history persistence")
					}
					receipt.Store(&api.EnvironmentRollbackReceipt{AttemptID: ref.AttemptID, RequestHash: ref.RequestHash, PromotionHash: ref.PromotionHash, PromotionID: p.Plan.PromotionID, ProjectID: p.Plan.ProjectID, FromEnvironmentID: p.Plan.FromID, EnvironmentID: p.Plan.EnvironmentID, AcceptedAt: time.Now().UTC()})
					writes.Add(1)
					w.WriteHeader(504)
					return
				}
				f.serve(w, r)
			}))
			defer backend.Close()
			opts := environmentTestOptions(backend, true)
			opts.planStore = store
			opts.planNamespace = "environment-receipt"
			manager := newHistoryEnvironment(t, backend, opts)
			planID = planHistoryEnvironment(t, manager, true)
			if _, err := manager.applyEnvironmentRollback(t.Context(), applyArgs{PlanID: planID}); err == nil {
				t.Fatal("lost reply succeeded")
			}
			ref := manager.environmentRollbackOutcomes[planID]
			encrypted, _ := os.ReadFile(path)
			for _, secret := range []string{ref.AttemptID, ref.RequestHash, "PRIVATE_ENV_REASON"} {
				if bytes.Contains(encrypted, []byte(secret)) {
					t.Fatal("plaintext lookup")
				}
			}
			// Advance the original fixture's clock without waiting for a 15-minute TTL.
			manager.environmentPlans[planID].ExpiresAt = time.Now().Add(-time.Minute)
			ref.Binding = environmentRollbackBinding(recordEnvironmentAction(manager.environmentPlans[planID]))
			manager.environmentRollbackOutcomes[planID] = ref
			if err := manager.persistPlans(); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			next, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			opts.planStore, opts.AllowWrites = next, false
			cs, ctx := connectConfiguredTestServer(t, backend, opts, protocol)
			args := getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: planID}
			before, _ := os.ReadFile(path)
			detail := callTool(t, ctx, cs, "gregale_get_environment_action", args, false)
			var view environmentActionView
			if json.Unmarshal(detail["action"], &view) != nil || view.PlanAvailable || view.Apply.State != "outcome_unknown" || view.Recovery == nil || view.Recovery.Outcome == nil || view.Recovery.Check.Tool != "" {
				t.Fatalf("missing expired recovery: %s", detail)
			}
			resource, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: view.Recovery.ResourceURI})
			if err != nil {
				t.Fatal(err)
			}
			recipe := recoveryResourceView(t, resource, view.Recovery.ResourceURI)
			if recipe.Workflow.Outcome == nil || recipe.Workflow.Review.Tool != "" || recipe.Workflow.Check.Tool != "" || recipe.Workflow.EnvironmentObservation == nil {
				t.Fatal("expired recipe changed authority")
			}
			prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_environment_plan_review", Arguments: map[string]string{"project": "shop", "environment": "production", "plan_id": planID}})
			if err != nil || len(prompt.Messages) != 1 || reads.Load() != 0 {
				t.Fatalf("passive discovery read native receipt: %v", err)
			}
			result := callTool(t, ctx, cs, "gregale_get_environment_action_outcome", args, false)
			body, _ := json.Marshal(result)
			if string(result["outcome"]) != `"verified_receipt"` || string(result["native_completion"]) != `"not_observed"` || bytes.Contains(body, []byte(ref.AttemptID)) || bytes.Contains(body, []byte(ref.RequestHash)) || bytes.Contains(body, []byte("PRIVATE_ENV_REASON")) {
				t.Fatalf("outcome=%s", body)
			}
			var observation environmentActionObservation
			if json.Unmarshal(result["observation"], &observation) != nil || observation.Tool != "gregale_get_environment_promotion" || observation.Arguments.PromotionID != "promotion-one" || observation.Arguments.Environment != "production" {
				t.Fatal("lost exact observation")
			}
			again := callTool(t, ctx, cs, "gregale_get_environment_action", args, false)
			if json.Unmarshal(again["action"], &view) != nil || view.Apply.State != "outcome_unknown" {
				t.Fatal("recovery rewrote recorded uncertainty")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) || writes.Load() != 1 || reads.Load() != 1 {
				t.Fatalf("recovery wrote state or replayed: writes=%d reads=%d", writes.Load(), reads.Load())
			}
		})
	}
}
func environmentRollbackOutcomeFixture(t *testing.T) (*management, string, *api.EnvironmentRollbackReceipt) {
	t.Helper()
	f, _ := newEnvironmentFixture(t)
	receipt := &api.EnvironmentRollbackReceipt{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/rollback-attempts/") {
			_ = json.NewEncoder(w).Encode(receipt)
			return
		}
		f.serve(w, r)
	}))
	t.Cleanup(backend.Close)
	store, _, _ := testStateStore(t)
	opts := environmentTestOptions(backend, true)
	opts.planStore = store
	opts.planNamespace = "outcome"
	manager := newHistoryEnvironment(t, backend, opts)
	id := planHistoryEnvironment(t, manager, true)
	p := manager.environmentPlans[id]
	if _, err := manager.prepareEnvironmentRollbackOutcome(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	p.attempted = true
	if err := manager.persistPlans(); err != nil {
		t.Fatal(err)
	}
	ref := manager.environmentRollbackOutcomes[id]
	*receipt = api.EnvironmentRollbackReceipt{AttemptID: ref.AttemptID, RequestHash: ref.RequestHash, PromotionHash: ref.PromotionHash, PromotionID: p.PromotionID, ProjectID: p.ProjectID, FromEnvironmentID: p.FromID, EnvironmentID: p.EnvironmentID, AcceptedAt: time.Now().UTC()}
	return manager, id, receipt
}
func TestEnvironmentRollbackOutcomeEvidenceAndGuards(t *testing.T) {
	for name, change := range map[string]func(*api.EnvironmentRollbackReceipt){
		"attempt": func(r *api.EnvironmentRollbackReceipt) { r.AttemptID = strings.Repeat("e", 32) }, "hash": func(r *api.EnvironmentRollbackReceipt) { r.RequestHash = strings.Repeat("e", 64) }, "promotion": func(r *api.EnvironmentRollbackReceipt) { r.PromotionID = "other" }, "promotion_hash": func(r *api.EnvironmentRollbackReceipt) { r.PromotionHash = strings.Repeat("e", 64) }, "project": func(r *api.EnvironmentRollbackReceipt) { r.ProjectID = "other" }, "source": func(r *api.EnvironmentRollbackReceipt) { r.FromEnvironmentID = "other" }, "target": func(r *api.EnvironmentRollbackReceipt) { r.EnvironmentID = "other" }, "time": func(r *api.EnvironmentRollbackReceipt) { r.AcceptedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			manager, id, receipt := environmentRollbackOutcomeFixture(t)
			change(receipt)
			value, err := manager.getEnvironmentActionOutcome(t.Context(), getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id})
			if err != nil {
				t.Fatal(err)
			}
			out := value.(*guardedRead).value.(environmentActionOutcome)
			if out.Outcome != "unresolved" || out.Reason != "receipt_unverified" || out.Receipt != nil || out.Observation != nil {
				t.Fatal("unverified receipt released")
			}
		})
	}
	for _, kind := range []string{"reference", "history", "retention", "storage", "source_consent", "target_consent", "workload"} {
		t.Run("final_"+kind, func(t *testing.T) {
			manager, id, _ := environmentRollbackOutcomeFixture(t)
			value, err := manager.getEnvironmentActionOutcome(t.Context(), getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id})
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "reference":
				delete(manager.environmentRollbackOutcomes, id)
			case "history":
				delete(manager.environmentActions, id)
			case "retention":
				entry := manager.environmentActions[id]
				entry.RetainedUntil = time.Now().Add(-time.Minute)
				manager.environmentActions[id] = entry
			case "storage":
				_ = manager.opts.planStore.Close()
			case "source_consent":
				delete(manager.opts.Environments.Named, "shop/staging")
			case "target_consent":
				delete(manager.opts.Environments.Named, "shop/production")
			case "workload":
				manager.opts.AllowedApps = map[string]string{"other": "other"}
			}
			if value.(*guardedRead).check(t.Context()) == nil {
				t.Fatal("changed final guard released outcome")
			}
		})
	}
}
func TestEnvironmentRollbackOutcomeLegacyAndSnapshot(t *testing.T) {
	for _, kind := range []string{"preserve", "version", "binding", "attempt", "orphan", "legacy", "expiry", "revocation"} {
		t.Run(kind, func(t *testing.T) {
			manager, id, _ := environmentRollbackOutcomeFixture(t)
			store := manager.opts.planStore
			ref := manager.environmentRollbackOutcomes[id]
			if kind == "legacy" {
				delete(manager.environmentRollbackOutcomes, id)
				if _, err := manager.prepareEnvironmentRollbackOutcome(t.Context(), manager.environmentPlans[id]); err != nil || len(manager.environmentRollbackOutcomes) != 0 {
					t.Fatal("legacy attempt got replacement identity")
				}
				return
			}
			if kind == "expiry" {
				delete(manager.environmentPlans, id)
				entry := manager.environmentActions[id]
				entry.RetainedUntil = time.Now().Add(-time.Minute)
				manager.environmentActions[id] = entry
				if err := manager.persistPlans(); err != nil || len(manager.environmentRollbackOutcomes) != 0 {
					t.Fatal("expired reference retained")
				}
				return
			}
			if kind == "revocation" {
				if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}); err != nil {
					t.Fatal(err)
				}
				var saved environmentRollbackSnapshot
				if err := store.read(environmentRollbackOutcomeNamespace+"outcome", &saved); err != nil || len(saved.Items) != 0 {
					t.Fatal("revoked lookup retained")
				}
				return
			}
			saved := environmentRollbackSnapshot{Version: 1, Items: map[string]environmentRollbackLookup{id: ref}}
			switch kind {
			case "version":
				saved.Version = 2
			case "binding":
				ref.Binding = strings.Repeat("f", 64)
				saved.Items[id] = ref
			case "attempt":
				ref.AttemptID = "bad"
				saved.Items[id] = ref
			case "orphan":
				delete(saved.Items, id)
				saved.Items[strings.Repeat("f", 64)] = ref
			}
			if err := store.update(environmentRollbackOutcomeNamespace+"outcome", saved); err != nil {
				t.Fatal(err)
			}
			if kind == "preserve" {
				if err := store.update("future/opaque", map[string]int{"version": 9}); err != nil {
					t.Fatal(err)
				}
			}
			opts := manager.opts
			opts.AllowWrites = false
			_, restored, err := newHistoryTestManagementServer(manager.client, opts)
			valid := kind == "preserve" || kind == "orphan"
			if valid != (err == nil) {
				t.Fatalf("restore %s: %v", kind, err)
			}
			if valid && kind == "orphan" && len(restored.environmentRollbackOutcomes) != 0 {
				t.Fatal("orphan retained")
			}
		})
	}
}
