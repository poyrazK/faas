// adr: 20261105131434
package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestScalingOutcomeLostReplyExpiry(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f := &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 1, values: map[string]string{}}
			store, path, key := testStateStore(t)
			var originalID string
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/v1/apps/alpha/scaling" {
					var refs scalingOutcomeSnapshot
					var history actionHistorySnapshot
					if err := store.read(scalingOutcomeNamespace+"scaling", &refs); err != nil {
						t.Error(err)
					}
					if err := store.read(scalingActionsNamespace+"scaling", &history); err != nil {
						t.Error(err)
					}
					if !validScalingOutcomeLookup(refs.Items[originalID]) || history.Items[originalID].Apply.State != "outcome_unknown" {
						t.Error("scaling POST preceded durable attempt and lookup")
					}
				}
				f.serveHTTP(w, r)
			}))
			defer backend.Close()
			opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, Toolsets: []string{ToolsetCore}, agentKey: true, planStore: store, planNamespace: "scaling"}
			_, manager, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			ram := 512
			value, err := manager.planScale(t.Context(), scalePlanArgs{App: "alpha", Reason: "PRIVATE_SCALING_REASON", RAMMB: &ram})
			if err != nil {
				t.Fatal(err)
			}
			id := value.(releasePlan).ID
			originalID = id
			manager.plans[id].ExpiresAt = time.Now().Add(3 * time.Second)
			f.failScalingAfterCommit = true
			if _, err = manager.applyOperation(t.Context(), applyArgs{PlanID: id}); err == nil {
				t.Fatal("lost response reported success")
			}
			ref := manager.scalingOutcomes[id]
			if !validScalingOutcomeLookup(ref) {
				t.Fatal("original lookup not retained before the request")
			}
			deadline := manager.plans[id].ExpiresAt
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			encrypted, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{ref.RequestID, ref.SettingsHash, "PRIVATE_SCALING_REASON"} {
				if bytes.Contains(encrypted, []byte(secret)) {
					t.Fatal("private data stored as plaintext")
				}
			}
			if delay := time.Until(deadline) + 10*time.Millisecond; delay > 0 {
				time.Sleep(delay)
			}
			next, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			opts.planStore, opts.AllowWrites = next, false
			cs, ctx := connectSelectedTestServer(t, backend, opts, protocol)
			args := getActionArgs{App: "alpha", PlanID: id}
			page := callTool(t, ctx, cs, "gregale_list_actions", listActionsArgs{App: "alpha"}, false)
			var items []actionView
			if json.Unmarshal(page["items"], &items) != nil || len(items) != 1 || items[0].PlanID != id || items[0].PlanAvailable || items[0].Recovery == nil || items[0].Recovery.Outcome == nil || items[0].Review != nil {
				t.Fatalf("history=%s", page)
			}
			resource, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: items[0].Recovery.ResourceURI})
			if err != nil {
				t.Fatal(err)
			}
			recipe := recoveryResourceView(t, resource, items[0].Recovery.ResourceURI)
			if recipe.Workflow.Review.Tool != "" || recipe.Workflow.Check.Tool != "" || recipe.Workflow.Outcome == nil || recipe.Workflow.Observation == nil || recipe.Workflow.Observation.Tool != "gregale_get_app" {
				t.Fatal("invalid expired scaling recipe")
			}
			out := callTool(t, ctx, cs, "gregale_get_action_outcome", args, false)
			body, _ := json.Marshal(out)
			if string(out["outcome"]) != `"verified_receipt"` || string(out["native_completion"]) != `"not_observed"` || bytes.Contains(body, []byte(ref.RequestID)) || bytes.Contains(body, []byte(ref.SettingsHash)) || bytes.Contains(body, []byte("PRIVATE_SCALING_REASON")) {
				t.Fatalf("outcome=%s", body)
			}
			f.mu.Lock()
			receipt := f.scalingReceipts[ref.RequestID]
			f.mu.Unlock()
			for name, change := range map[string]func(*api.AppScalingReceipt){
				"settings":    func(r *api.AppScalingReceipt) { r.Settings.RAMMB++ },
				"request":     func(r *api.AppScalingReceipt) { r.RequestID = uuid.NewString() },
				"app":         func(r *api.AppScalingReceipt) { r.AppID = "different" },
				"environment": func(r *api.AppScalingReceipt) { r.EnvironmentID = "different" },
				"scope":       func(r *api.AppScalingReceipt) { r.Scope = "staging" },
				"revision":    func(r *api.AppScalingReceipt) { r.PreviousRevision++ },
				"timestamp":   func(r *api.AppScalingReceipt) { r.CommittedAt = time.Time{} },
			} {
				bad := receipt
				change(&bad)
				f.mu.Lock()
				f.scalingReceipts[ref.RequestID] = bad
				f.mu.Unlock()
				out = callTool(t, ctx, cs, "gregale_get_action_outcome", args, false)
				if string(out["outcome"]) != `"unresolved"` || string(out["reason"]) != `"receipt_unverified"` {
					t.Fatalf("%s mismatch=%s", name, out)
				}
			}
			f.mu.Lock()
			delete(f.scalingReceipts, ref.RequestID)
			f.mu.Unlock()
			out = callTool(t, ctx, cs, "gregale_get_action_outcome", args, false)
			if string(out["outcome"]) != `"unresolved"` || string(out["reason"]) != `"receipt_unavailable"` {
				t.Fatalf("missing=%s", out)
			}
			otherOpts := opts
			otherOpts.planNamespace = "another-grant"
			other, otherCtx := connectSelectedTestServer(t, backend, otherOpts, protocol)
			if code := toolCode(t, callTool(t, otherCtx, other, "gregale_get_action_outcome", args, true)); code != "action_not_found" {
				t.Fatalf("other grant=%s", code)
			}
			deniedOpts := opts
			deniedOpts.AllowedApps = map[string]string{"alpha": "different-app"}
			denied, deniedCtx := connectSelectedTestServer(t, backend, deniedOpts, protocol)
			if code := toolCode(t, callTool(t, deniedCtx, denied, "gregale_get_action_outcome", args, true)); code != "app_access_denied" {
				t.Fatalf("changed app=%s", code)
			}
			f.mu.Lock()
			writes := f.patches
			f.mu.Unlock()
			if writes != 1 {
				t.Fatalf("recovery performed %d writes", writes)
			}
		})
	}
}

func TestScalingOutcomeCommittedAndFinalGuard(t *testing.T) {
	m, _ := newOperationFixture(t)
	store, _, _ := testStateStore(t)
	m.opts.planStore, m.opts.planNamespace = store, "scaling-guard"
	ram := 512
	plan, err := m.planScale(t.Context(), scalePlanArgs{App: "alpha", Reason: "scale", RAMMB: &ram})
	if err != nil {
		t.Fatal(err)
	}
	id := plan.(releasePlan).ID
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatal(err)
	}
	entry := m.actions[id]
	if entry.Apply.State != "committed" || entry.Apply.Receipt == nil || entry.Apply.Receipt.ScalingRevision == nil {
		t.Fatalf("committed history=%+v", entry.Apply)
	}
	value, err := m.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
	if err != nil {
		t.Fatal(err)
	}
	guard := value.(*guardedRead)
	if guard.value.(actionOutcome).Outcome != "verified_receipt" {
		t.Fatal("committed receipt not verified")
	}
	_, workflow, err := m.savedPromptWorkflow(t.Context(), getActionArgs{App: "alpha", PlanID: id}, false)
	if err != nil || workflow.Review.Tool != "" || workflow.Check.Tool != "" || workflow.Outcome == nil {
		t.Fatalf("live scaling recipe: %v", err)
	}
	ref := m.scalingOutcomes[id]
	changed := ref
	changed.SettingsHash = strings.Repeat("a", 64)
	m.scalingOutcomes[id] = changed
	if err := guard.check(t.Context()); err == nil {
		t.Fatal("changed reference released after native read")
	}
	m.scalingOutcomes[id] = ref
	delete(m.actions, id)
	if err := guard.check(t.Context()); err == nil {
		t.Fatal("deleted history released after native read")
	}
}

func TestScalingOutcomeNamedEnvironmentBinding(t *testing.T) {
	settings := api.AppScalingSettings{RAMMB: 512, CPUMillicores: 500, MaxConcurrency: 10, MinInstances: 1}
	entry := actionRecord{AppID: "app-one", Scope: "staging", ProjectID: "project-one", EnvironmentID: "env-one"}
	ref := scalingOutcomeLookup{RequestID: uuid.NewString(), ExpectedRevision: 7, SettingsHash: scalingSettingsHash(settings)}
	receipt := api.AppScalingReceipt{AppScalingSnapshot: api.AppScalingSnapshot{AppID: entry.AppID, Scope: entry.Scope, EnvironmentID: entry.EnvironmentID, Revision: 7, Settings: settings}, RequestID: ref.RequestID, PreviousRevision: 7, CommittedAt: time.Now()}
	if !scalingOutcomeMatches(entry, ref, receipt) {
		t.Fatal("valid named receipt rejected")
	}
	for name, change := range map[string]func(*api.AppScalingReceipt){
		"app":         func(r *api.AppScalingReceipt) { r.AppID = "other" },
		"scope":       func(r *api.AppScalingReceipt) { r.Scope = "production" },
		"environment": func(r *api.AppScalingReceipt) { r.EnvironmentID = "env-other" },
		"request":     func(r *api.AppScalingReceipt) { r.RequestID = uuid.NewString() },
		"revision":    func(r *api.AppScalingReceipt) { r.Revision-- },
		"settings":    func(r *api.AppScalingReceipt) { r.Settings.CPUMillicores++ },
	} {
		bad := receipt
		change(&bad)
		if scalingOutcomeMatches(entry, ref, bad) {
			t.Errorf("%s mismatch was accepted", name)
		}
	}
	entry.Scope, entry.EnvironmentID = api.DefaultEnvScope, ""
	receipt.Scope, receipt.EnvironmentID = api.DefaultEnvScope, ""
	if scalingOutcomeMatches(entry, ref, receipt) {
		t.Fatal("default-scope revision did not advance")
	}
}

func TestScalingOutcomeGrantRevocation(t *testing.T) {
	store, _, _ := testStateStore(t)
	lookup := scalingOutcomeSnapshot{Version: 1, Items: map[string]scalingOutcomeLookup{strings.Repeat("a", 64): {RequestID: uuid.NewString(), ExpectedRevision: 1, SettingsHash: strings.Repeat("b", 64), Binding: strings.Repeat("c", 64)}}}
	history := actionHistorySnapshot{Version: 1, Items: map[string]actionRecord{}}
	if err := store.updateMany(map[string]any{scalingOutcomeNamespace + "revoked": lookup, scalingOutcomeNamespace + "active": lookup, scalingActionsNamespace + "revoked": history, scalingActionsNamespace + "active": history}); err != nil {
		t.Fatal(err)
	}
	if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{"active": {}}}); err != nil {
		t.Fatal(err)
	}
	var revoked, active scalingOutcomeSnapshot
	if err := store.read(scalingOutcomeNamespace+"revoked", &revoked); err != nil || len(revoked.Items) != 0 {
		t.Fatal("revocation retained scaling reference")
	}
	if err := store.read(scalingOutcomeNamespace+"active", &active); err != nil || !reflect.DeepEqual(active, lookup) {
		t.Fatal("revocation removed another grant")
	}
	var old, current actionHistorySnapshot
	if err := store.read(scalingActionsNamespace+"revoked", &old); err != nil || old.Version != 0 {
		t.Fatal("revocation retained scaling history")
	}
	if err := store.read(scalingActionsNamespace+"active", &current); err != nil || current.Version != 1 {
		t.Fatal("revocation removed another grant's history")
	}
}

func TestScalingOutcomeLegacyPlanWithoutCheckpoint(t *testing.T) {
	m, f := newOperationFixture(t)
	store, _, _ := testStateStore(t)
	m.opts.planStore, m.opts.planNamespace = store, "legacy-scaling"
	ram := 512
	value, err := m.planScale(t.Context(), scalePlanArgs{App: "alpha", Reason: "scale", RAMMB: &ram})
	if err != nil {
		t.Fatal(err)
	}
	id := value.(releasePlan).ID
	m.plans[id].Operation.ScalingBefore = nil
	m.plans[id].attempted = true
	if err := m.persistPlans(); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.scalingOutcomes[id]; ok {
		t.Fatal("legacy plan acquired an invented outcome reference")
	}
	_, restored, err := newManagementServer(m.client, m.opts)
	if err != nil {
		t.Fatalf("legacy plan prevented restart: %v", err)
	}
	result, err := restored.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
	if err != nil {
		t.Fatal(err)
	}
	out := result.(*guardedRead).value.(actionOutcome)
	if out.Outcome != "unresolved" || out.Reason != "lookup_unavailable" {
		t.Fatalf("legacy outcome=%+v", out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.patches != 0 {
		t.Fatal("legacy recovery wrote scaling state")
	}
}
