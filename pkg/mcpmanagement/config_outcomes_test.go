// adr: 20261105140140
package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestConfigOutcomeLostReplyAndRestartExpiry(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		for _, restart := range []bool{false, true} {
			t.Run(protocol+map[bool]string{false: "/lost_batch", true: "/restart_denied"}[restart], func(t *testing.T) {
				f := &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 1, values: map[string]string{}}
				store, path, key := testStateStore(t)
				var originalID string
				var receiptReads, restartCalls atomic.Int32
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost && r.URL.Path == "/v1/apps/alpha/env/batch" {
						var refs configOutcomeSnapshot
						var history actionHistorySnapshot
						if err := store.read(configOutcomeNamespace+"config", &refs); err != nil {
							t.Error(err)
						}
						if err := store.read(configActionsNamespace+"config", &history); err != nil {
							t.Error(err)
						}
						if !validConfigOutcomeLookup(refs.Items[originalID]) || history.Items[originalID].Apply.State != "outcome_unknown" {
							t.Error("batch preceded durable attempt and lookup")
						}
					}
					if r.URL.Path == "/v1/apps/alpha/restart" {
						restartCalls.Add(1)
					}
					if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/apps/alpha/env/batch/requests/") {
						receiptReads.Add(1)
						f.mu.Lock()
						receipt, ok := f.batchReceipts[strings.TrimPrefix(r.URL.Path, "/v1/apps/alpha/env/batch/requests/")]
						var original api.EnvBatchRequest
						if len(f.batchBodies) > 0 {
							_ = json.Unmarshal([]byte(f.batchBodies[0]), &original)
						}
						f.mu.Unlock()
						if !ok || original.ReceiptProof == "" || r.Header.Get(api.EnvBatchReceiptProofHeader) != original.ReceiptProof {
							w.WriteHeader(http.StatusNotFound)
							return
						}
						_ = json.NewEncoder(w).Encode(receipt)
						return
					}
					f.serveHTTP(w, r)
				}))
				defer backend.Close()
				opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, Toolsets: []string{ToolsetCore}, agentKey: true, Capabilities: []string{api.ScopeEnvWrite}, planStore: store, planNamespace: "config"}
				_, manager, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
				if err != nil {
					t.Fatal(err)
				}
				value, err := manager.planNativeEnvChange(t.Context(), planEnvChangeArgs{App: "alpha", Reason: "PRIVATE_REASON", Values: map[string]string{"PUBLIC_MODE": "private-setting-value"}, Restart: restart})
				if err != nil {
					t.Fatal(err)
				}
				id := value.(releasePlan).ID
				originalID = id
				manager.plans[id].ExpiresAt = time.Now().Add(3 * time.Second)
				f.failBatchAfterCommit, f.denyRestart = !restart, restart
				if _, err = manager.applyOperation(t.Context(), applyArgs{PlanID: id}); err == nil {
					t.Fatal("interruption reported success")
				}
				ref := manager.configOutcomes[id]
				if !validConfigOutcomeLookup(ref) {
					t.Fatal("original lookup missing")
				}
				deadline := manager.plans[id].ExpiresAt
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				encrypted, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				for _, secret := range []string{ref.RequestID, ref.ReceiptProof, ref.KeysHash, "PRIVATE_REASON", "PUBLIC_MODE", "private-setting-value"} {
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
				if json.Unmarshal(page["items"], &items) != nil || len(items) != 1 || items[0].PlanID != id || items[0].PlanAvailable || items[0].Recovery == nil || items[0].Recovery.Outcome == nil || items[0].Config == nil || items[0].Config.RestartRequested != restart {
					t.Fatalf("history=%s", page)
				}
				resource, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: items[0].Recovery.ResourceURI})
				if err != nil {
					t.Fatal(err)
				}
				recipe := recoveryResourceView(t, resource, items[0].Recovery.ResourceURI)
				if recipe.Workflow.Review.Tool != "" || recipe.Workflow.Check.Tool != "" || recipe.Workflow.Outcome == nil || recipe.Workflow.OutcomeKind != "configuration" || receiptReads.Load() != 0 {
					t.Fatal("invalid or active recovery recipe")
				}
				before, _ := os.ReadFile(path)
				out := callTool(t, ctx, cs, "gregale_get_action_outcome", args, false)
				body, _ := json.Marshal(out)
				wantRestart := `"not_requested"`
				if restart {
					wantRestart = `"not_observed"`
				}
				if string(out["outcome"]) != `"verified_receipt"` || string(out["restart_status"]) != wantRestart || string(out["native_completion"]) != `"not_observed"` {
					t.Fatalf("outcome=%s", body)
				}
				for _, secret := range []string{ref.RequestID, ref.ReceiptProof, ref.KeysHash, "PRIVATE_REASON", "PUBLIC_MODE", "private-setting-value"} {
					if bytes.Contains(body, []byte(secret)) {
						t.Fatal("outcome exposed private reference or configuration")
					}
				}
				_, restored, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
				if err != nil {
					t.Fatal(err)
				}
				guarded, err := restored.getActionOutcome(ctx, args)
				if err != nil {
					t.Fatal(err)
				}
				changed := restored.configOutcomes[id]
				changed.KeysHash = strings.Repeat("a", 64)
				restored.configOutcomes[id] = changed
				if guarded.(*guardedRead).check(ctx) == nil {
					t.Fatal("changed reference released after native read")
				}
				f.mu.Lock()
				saved := f.batchReceipts[ref.RequestID]
				bad := saved
				bad.UpdatedKeys = []string{"OTHER"}
				f.batchReceipts[ref.RequestID] = bad
				f.mu.Unlock()
				out = callTool(t, ctx, cs, "gregale_get_action_outcome", args, false)
				if string(out["outcome"]) != `"unresolved"` || string(out["reason"]) != `"receipt_unverified"` {
					t.Fatalf("mismatch=%s", out)
				}
				f.mu.Lock()
				delete(f.batchReceipts, ref.RequestID)
				f.mu.Unlock()
				out = callTool(t, ctx, cs, "gregale_get_action_outcome", args, false)
				if string(out["outcome"]) != `"unresolved"` || string(out["reason"]) != `"receipt_unavailable"` {
					t.Fatalf("missing=%s", out)
				}
				otherOpts := opts
				otherOpts.planNamespace = "another-grant"
				other, otherCtx := connectSelectedTestServer(t, backend, otherOpts, protocol)
				beforeDenied := receiptReads.Load()
				if code := toolCode(t, callTool(t, otherCtx, other, "gregale_get_action_outcome", args, true)); code != "action_not_found" {
					t.Fatalf("other grant=%s", code)
				}
				deniedOpts := opts
				deniedOpts.AllowedApps = map[string]string{"alpha": "different-app"}
				denied, deniedCtx := connectSelectedTestServer(t, backend, deniedOpts, protocol)
				if code := toolCode(t, callTool(t, deniedCtx, denied, "gregale_get_action_outcome", args, true)); code != "app_access_denied" {
					t.Fatalf("changed app=%s", code)
				}
				if receiptReads.Load() != beforeDenied {
					t.Fatal("denied recovery read native receipt")
				}
				after, _ := os.ReadFile(path)
				f.mu.Lock()
				writes := f.puts
				f.mu.Unlock()
				if !bytes.Equal(before, after) || writes != 1 || restartCalls.Load() != map[bool]int32{false: 0, true: 1}[restart] {
					t.Fatal("recovery changed state or repeated a write/restart")
				}
			})
		}
	}
}

func TestConfigOutcomeLegacyUnpinnedPlan(t *testing.T) {
	m, f := newOperationFixture(t)
	store, _, _ := testStateStore(t)
	m.opts.planStore, m.opts.planNamespace = store, "legacy-config"
	value, err := m.planNativeEnvChange(t.Context(), planEnvChangeArgs{App: "alpha", Reason: "legacy", Values: map[string]string{"MODE": "old"}})
	if err != nil {
		t.Fatal(err)
	}
	id := value.(releasePlan).ID
	m.plans[id].Operation.EnvIdentityPinned = false
	m.plans[id].attempted = true
	if err := m.persistPlans(); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.configOutcomes[id]; ok {
		t.Fatal("legacy plan acquired an invented outcome reference")
	}
	_, restored, err := newManagementServer(m.client, m.opts)
	if err != nil {
		t.Fatal(err)
	}
	value, err = restored.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
	if err != nil {
		t.Fatal(err)
	}
	out := value.(*guardedRead).value.(actionOutcome)
	if out.Outcome != "unresolved" || out.Reason != "lookup_unavailable" {
		t.Fatalf("legacy outcome=%+v", out)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.puts != 0 || f.restarts != 0 {
		t.Fatal("legacy recovery performed a mutation")
	}
}

func TestConfigOutcomeGrantRevocation(t *testing.T) {
	store, _, _ := testStateStore(t)
	lookup := configOutcomeSnapshot{Version: 1, Items: map[string]configOutcomeLookup{strings.Repeat("a", 64): {RequestID: uuid.NewString(), ReceiptProof: strings.Repeat("d", 64), ExpectedRevision: 1, KeysHash: strings.Repeat("b", 64), Binding: strings.Repeat("c", 64)}}}
	history := actionHistorySnapshot{Version: 1, Items: map[string]actionRecord{}}
	if err := store.updateMany(map[string]any{configOutcomeNamespace + "revoked": lookup, configOutcomeNamespace + "active": lookup, configActionsNamespace + "revoked": history, configActionsNamespace + "active": history}); err != nil {
		t.Fatal(err)
	}
	if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{"active": {}}}); err != nil {
		t.Fatal(err)
	}
	var revoked, active configOutcomeSnapshot
	if err := store.read(configOutcomeNamespace+"revoked", &revoked); err != nil || len(revoked.Items) != 0 {
		t.Fatal("revocation retained configuration reference")
	}
	if err := store.read(configOutcomeNamespace+"active", &active); err != nil || !reflect.DeepEqual(active, lookup) {
		t.Fatal("revocation removed another grant")
	}
	var old, current actionHistorySnapshot
	if err := store.read(configActionsNamespace+"revoked", &old); err != nil || old.Version != 0 {
		t.Fatal("revocation retained configuration history")
	}
	if err := store.read(configActionsNamespace+"active", &current); err != nil || current.Version != 1 {
		t.Fatal("revocation removed another grant's history")
	}
}

func TestConfigOutcomeNamedBinding(t *testing.T) {
	entry := actionRecord{AppID: "app-one", Scope: "staging", EnvironmentID: "environment-one"}
	ref := configOutcomeLookup{RequestID: "request", ExpectedRevision: 4, KeysHash: configKeysHash([]string{"MODE"})}
	receipt := api.EnvBatchResponse{RequestID: ref.RequestID, AppID: entry.AppID, Scope: entry.Scope, EnvironmentID: entry.EnvironmentID, UpdatedKeys: []string{"MODE"}, ConfigChange: api.ConfigChangeReceipt{Revision: 5}}
	if !configOutcomeMatches(entry, ref, receipt) {
		t.Fatal("valid receipt rejected")
	}
	for name, change := range map[string]func(*api.EnvBatchResponse){"app": func(r *api.EnvBatchResponse) { r.AppID = "other" }, "scope": func(r *api.EnvBatchResponse) { r.Scope = "production" }, "environment": func(r *api.EnvBatchResponse) { r.EnvironmentID = "recreated" }, "revision": func(r *api.EnvBatchResponse) { r.ConfigChange.Revision++ }, "request": func(r *api.EnvBatchResponse) { r.RequestID = "other" }, "keys": func(r *api.EnvBatchResponse) { r.UpdatedKeys = []string{"OTHER"} }, "deleted": func(r *api.EnvBatchResponse) { r.DeletedKeys = []string{"MODE"} }} {
		bad := receipt
		change(&bad)
		if configOutcomeMatches(entry, ref, bad) {
			t.Errorf("%s mismatch accepted", name)
		}
	}
}
