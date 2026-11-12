// adr: 20261105152500
package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestLegacyConfigRollbackCannotDowngrade(t *testing.T) {
	m := &management{}
	for _, operation := range []*operationPlan{{Kind: "config_rollback"}, {Kind: "config_rollback_guarded"}} {
		if _, err := m.runOperation(context.Background(), &releasePlan{Operation: operation}, false); err == nil {
			t.Fatalf("unguarded %s accepted", operation.Kind)
		}
	}
}

func TestConfigRollbackOutcomeAfterLostReplyAndExpiry(t *testing.T) {
	f := &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 3, values: map[string]string{"MODE": "newer"}}
	store, path, key := testStateStore(t)
	var request api.ConfigRollbackRequest
	var writes atomic.Int32
	var noChange atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/alpha/config/revisions/2":
			_ = json.NewEncoder(w).Encode(api.ConfigRevisionDetail{ConfigRevision: api.ConfigRevision{Revision: 2, Scope: "default"}, Variables: map[string]string{"MODE": "before"}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/alpha/config/rollback":
			writes.Add(1)
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if request.ExpectedRevision == nil || *request.ExpectedRevision != 3 || request.ExpectedAppID != f.base.app.ID || request.RequestID == "" || !api.ValidEnvBatchReceiptProof(request.ReceiptProof) {
				t.Error("rollback omitted original native guards")
			}
			api.WriteProblem(w, api.NewProblem(502, "transport_error", "response lost", "inspect original outcome "+request.ReceiptProof))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/apps/alpha/config/rollback/requests/"):
			if request.RequestID == "" || r.Header.Get(api.EnvBatchReceiptProofHeader) != request.ReceiptProof {
				w.WriteHeader(404)
				return
			}
			revision := int64(4)
			if noChange.Load() {
				revision = 3
			}
			_ = json.NewEncoder(w).Encode(api.ConfigRollbackReceipt{RequestID: request.RequestID, AppID: f.base.app.ID, Scope: "default", RestoredRevision: 2, ConfigRevision: revision, Changed: !noChange.Load()})
		default:
			f.serveHTTP(w, r)
		}
	}))
	defer backend.Close()
	opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, Toolsets: []string{ToolsetOperate}, agentKey: true, Capabilities: []string{api.ScopeEnvWrite}, planStore: store, planNamespace: "config-rollback"}
	_, manager, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	value, err := manager.planConfigRollback(t.Context(), configRollbackArgs{App: "alpha", Revision: 2, Reason: "reviewed rollback"})
	if err != nil {
		t.Fatal(err)
	}
	id := value.(releasePlan).ID
	manager.plans[id].ExpiresAt = time.Now().Add(2 * time.Second)
	if _, err := manager.applyOperation(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("lost response reported success")
	} else if strings.Contains(err.Error(), request.ReceiptProof) {
		t.Fatal("native error exposed the private receipt proof")
	}
	ref := manager.configRollbacks[id]
	if !validConfigRollbackLookup(ref) || ref.RequestID != request.RequestID || ref.ReceiptProof != request.ReceiptProof {
		t.Fatal("original lookup was not retained")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), ref.ReceiptProof) || strings.Contains(string(data), ref.RequestID) {
		t.Fatal("private lookup was persisted as plaintext")
	}
	time.Sleep(time.Until(manager.plans[id].ExpiresAt) + 20*time.Millisecond)
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	opts.planStore, opts.AllowWrites = next, false
	cs, ctx := connectSelectedTestServer(t, backend, opts, "2025-11-25")
	result := callTool(t, ctx, cs, "gregale_get_action_outcome", getActionArgs{App: "alpha", PlanID: id}, false)
	if string(result["outcome"]) != `"verified_receipt"` || string(result["config_changed"]) != "true" || strings.Contains(string(result["receipt"]), request.ReceiptProof) {
		t.Fatalf("outcome=%s", result)
	}
	noChange.Store(true)
	result = callTool(t, ctx, cs, "gregale_get_action_outcome", getActionArgs{App: "alpha", PlanID: id}, false)
	if string(result["outcome"]) != `"verified_receipt"` || string(result["config_changed"]) != "false" {
		t.Fatalf("no-op outcome=%s", result)
	}
	if writes.Load() != 1 {
		t.Fatalf("repeated rollback %d times", writes.Load())
	}
}
