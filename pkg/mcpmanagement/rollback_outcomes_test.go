// adr: 20261026090000
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

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRollbackOutcomeLostReplyExpiry(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f, _, _ := newReleaseFixture(t, true)
			store, path, key := testStateStore(t)
			var planID string
			var receipt atomic.Pointer[api.RollbackOperation]
			var reads, writes atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/v1/apps/alpha/rollbacks/") {
					reads.Add(1)
					op := receipt.Load()
					if r.Method != "GET" || r.Header.Get(api.RollbackAttemptHeader) != "" || r.Header.Get("Idempotency-Key") != "" || op == nil || !strings.HasSuffix(r.URL.Path, op.ID) {
						w.WriteHeader(404)
						return
					}
					_ = json.NewEncoder(w).Encode(op)
					return
				}
				if r.Method == "POST" && r.URL.Path == "/v1/apps/alpha/rollback" {
					var req api.RollbackRequest
					if json.NewDecoder(r.Body).Decode(&req) != nil {
						t.Error("invalid request")
						w.WriteHeader(400)
						return
					}
					var refs rollbackOutcomeSnapshot
					var plans map[string]storedPlan
					if err := store.read(rollbackOutcomeNamespace+"rollback", &refs); err != nil {
						t.Error(err)
					}
					if err := store.read("plans/rollback", &plans); err != nil {
						t.Error(err)
					}
					ref := refs.Items[planID]
					if !plans[planID].Attempted || !validRollbackOutcomeLookup(ref) || ref.AttemptID != r.Header.Get(api.RollbackAttemptHeader) || ref.RequestHash != api.RollbackRequestHash("app-one", req) {
						t.Error("POST preceded atomic attempt persistence")
					}
					writes.Add(1)
					receipt.Store(&api.RollbackOperation{ID: uuid.MustParse(ref.AttemptID).String(), AppID: "app-one", Scope: "default", TargetDeploymentID: *req.TargetDeploymentID, CurrentDeploymentID: *req.ExpectedCurrentDeploymentID, Reason: req.Reason, Status: "preparing", CreatedAt: time.Now().UTC()})
					api.WriteProblem(w, api.ErrCapacity("lost reply"))
					return
				}
				f.serveHTTP(w, r)
			}))
			defer backend.Close()
			opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "rollback"}
			_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			value, err := m.planRollback(t.Context(), planRollbackArgs{App: "alpha", TargetDeploymentID: "release-old", Reason: "PRIVATE_ORIGINAL_REASON"})
			if err != nil {
				t.Fatal(err)
			}
			planID = value.(releasePlan).ID
			if _, err = m.applyRollback(t.Context(), applyArgs{PlanID: planID}); err == nil {
				t.Fatal("lost reply succeeded")
			}
			ref := m.rollbackOutcomes[planID]
			encrypted, _ := os.ReadFile(path)
			for _, secret := range []string{ref.AttemptID, ref.RequestHash, "PRIVATE_ORIGINAL_REASON"} {
				if bytes.Contains(encrypted, []byte(secret)) {
					t.Fatal("plaintext lookup")
				}
			}
			op := *receipt.Load()
			op.Status = "failed"
			op.Code = "later_failure"
			receipt.Store(&op)
			f.mu.Lock()
			f.current = "release-later"
			f.mu.Unlock()
			m.plans[planID].ExpiresAt = time.Now().Add(-time.Minute)
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
			defer next.Close()
			opts.planStore, opts.AllowWrites = next, false
			cs, ctx := connectConfiguredTestServer(t, backend, opts, protocol)
			args := getActionArgs{App: "alpha", PlanID: planID}
			before, _ := os.ReadFile(path)
			history := callTool(t, ctx, cs, "gregale_get_action", args, false)
			var view actionView
			if json.Unmarshal(history["action"], &view) != nil || view.Apply.State != "outcome_unknown" || view.PlanAvailable || view.Recovery == nil || view.Recovery.Outcome == nil {
				t.Fatalf("history=%s", history)
			}
			resource, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: view.Recovery.ResourceURI})
			if err != nil {
				t.Fatal(err)
			}
			recipe := recoveryResourceView(t, resource, view.Recovery.ResourceURI)
			if recipe.Workflow.Review.Tool != "" || recipe.Workflow.Check.Tool != "" || recipe.Workflow.Outcome == nil {
				t.Fatal("expired recipe granted authority")
			}
			prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_saved_plan_review", Arguments: map[string]string{"app": "alpha", "plan_id": planID}})
			if err != nil || len(prompt.Messages) != 1 || reads.Load() != 0 {
				t.Fatalf("passive discovery: %v reads=%d", err, reads.Load())
			}
			result := callTool(t, ctx, cs, "gregale_get_action_outcome", args, false)
			body, _ := json.Marshal(result)
			if string(result["outcome"]) != "\"verified_receipt\"" || string(result["native_completion"]) != "\"not_observed\"" || bytes.Contains(body, []byte("PRIVATE_ORIGINAL_REASON")) || bytes.Contains(body, []byte(ref.RequestHash)) {
				t.Fatalf("outcome=%s", body)
			}
			var observation savedPlanReference
			if json.Unmarshal(result["observation"], &observation) != nil || observation.Tool != "gregale_get_rollback" {
				t.Fatalf("observation=%s", result["observation"])
			}
			observed := callTool(t, ctx, cs, observation.Tool, observation.Arguments, false)
			if string(observed["status"]) != "\"failed\"" {
				t.Fatalf("operation completion was not independently observed: %s", observed)
			}
			again := callTool(t, ctx, cs, "gregale_get_action", args, false)
			if json.Unmarshal(again["action"], &view) != nil || view.Apply.State != "outcome_unknown" {
				t.Fatal("recovery rewrote history")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) || writes.Load() != 1 {
				t.Fatalf("recovery wrote state or replayed: writes=%d", writes.Load())
			}
		})
	}
}

func rollbackOutcomeFixture(t *testing.T) (*management, string, *api.RollbackOperation) {
	t.Helper()
	f, _, _ := newReleaseFixture(t, true)
	op := &api.RollbackOperation{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/rollbacks/") {
			_ = json.NewEncoder(w).Encode(op)
			return
		}
		f.serveHTTP(w, r)
	}))
	t.Cleanup(backend.Close)
	store, _, _ := testStateStore(t)
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "rollback"})
	if err != nil {
		t.Fatal(err)
	}
	value, err := m.planRollback(t.Context(), planRollbackArgs{App: "alpha", TargetDeploymentID: "release-old", Reason: "PRIVATE_REASON"})
	if err != nil {
		t.Fatal(err)
	}
	id := value.(releasePlan).ID
	p := m.plans[id]
	if _, err = m.prepareRollbackOutcome(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	p.attempted = true
	if err = m.persistPlans(); err != nil {
		t.Fatal(err)
	}
	*op = api.RollbackOperation{ID: uuid.MustParse(m.rollbackOutcomes[id].AttemptID).String(), AppID: p.AppID, Scope: p.Scope, TargetDeploymentID: p.TargetDeploymentID, CurrentDeploymentID: p.ExpectedCurrentID, Reason: p.Reason, Status: "preparing", CreatedAt: time.Now().UTC()}
	return m, id, op
}
func TestRollbackOutcomeEvidenceGuards(t *testing.T) {
	tests := map[string]func(*api.RollbackOperation){"operation": func(r *api.RollbackOperation) { r.ID = uuid.NewString() }, "app": func(r *api.RollbackOperation) { r.AppID = "other" }, "target": func(r *api.RollbackOperation) { r.TargetDeploymentID = "other" }, "current": func(r *api.RollbackOperation) { r.CurrentDeploymentID = "other" }, "reason": func(r *api.RollbackOperation) { r.Reason = "changed" }, "scope": func(r *api.RollbackOperation) { r.Scope = "staging" }, "service": func(r *api.RollbackOperation) { r.Service = true }, "plain": func(r *api.RollbackOperation) { r.Trigger = "manual" }, "accepted_at": func(r *api.RollbackOperation) { r.CreatedAt = time.Time{} }}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			m, id, r := rollbackOutcomeFixture(t)
			change(r)
			v, err := m.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
			if err != nil {
				t.Fatal(err)
			}
			out := v.(*guardedRead).value.(actionOutcome)
			if out.Outcome != "unresolved" || out.Reason != "receipt_unverified" || out.Receipt != nil || out.Observation != nil {
				t.Fatalf("accepted malformed receipt: %+v", out)
			}
		})
	}
}
func TestRollbackOutcomeFinalGuards(t *testing.T) {
	for _, kind := range []string{"reference", "history", "retention", "storage", "consent"} {
		t.Run(kind, func(t *testing.T) {
			m, id, _ := rollbackOutcomeFixture(t)
			v, err := m.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "reference":
				delete(m.rollbackOutcomes, id)
			case "history":
				delete(m.actions, id)
			case "retention":
				e := m.actions[id]
				e.RetainedUntil = time.Now().Add(-time.Second)
				m.actions[id] = e
			case "storage":
				_ = m.opts.planStore.Close()
			case "consent":
				m.opts.AllowedApps = map[string]string{"other": "other"}
			}
			if err = v.(*guardedRead).check(t.Context()); err == nil {
				t.Fatal("read survived changed final guard")
			}
		})
	}
}
func TestRollbackOutcomeLegacyAndPruning(t *testing.T) {
	m, id, _ := rollbackOutcomeFixture(t)
	p := m.plans[id]
	delete(m.rollbackOutcomes, id)
	if _, err := m.prepareRollbackOutcome(t.Context(), p); err != nil || len(m.rollbackOutcomes) != 0 {
		t.Fatal("backfilled legacy attempt")
	}
	p.attempted = false
	p.RolloutMode = api.ExecutionModeService
	if _, err := m.prepareRollbackOutcome(t.Context(), p); err != nil || len(m.rollbackOutcomes) != 0 {
		t.Fatal("minted service recovery")
	}
	m, id, _ = rollbackOutcomeFixture(t)
	ref := m.rollbackOutcomes[id]
	ref.RequestHash = strings.Repeat("f", 64)
	m.rollbackOutcomes[id] = ref
	if err := m.persistPlans(); err == nil {
		t.Fatal("accepted changed original request")
	}
	m, id, _ = rollbackOutcomeFixture(t)
	delete(m.plans, id)
	e := m.actions[id]
	e.RetainedUntil = time.Now().Add(-time.Second)
	m.actions[id] = e
	if err := m.persistPlans(); err != nil || len(m.rollbackOutcomes) != 0 {
		t.Fatalf("retained expired lookup: %v", err)
	}
}

func TestRollbackOutcomeSnapshotCompatibility(t *testing.T) {
	for _, change := range []string{"preserve", "n_minus_one", "version", "binding", "attempt", "orphan"} {
		t.Run(change, func(t *testing.T) {
			m, id, _ := rollbackOutcomeFixture(t)
			store := m.opts.planStore
			var snapshot rollbackOutcomeSnapshot
			if err := store.read(rollbackOutcomeNamespace+"rollback", &snapshot); err != nil {
				t.Fatal(err)
			}
			ref := snapshot.Items[id]
			switch change {
			case "version":
				snapshot.Version = 2
			case "binding":
				ref.Binding = strings.Repeat("f", 64)
			case "attempt":
				ref.AttemptID = "bad"
			case "orphan":
				delete(snapshot.Items, id)
				snapshot.Items[strings.Repeat("a", 64)] = ref
			case "n_minus_one":
				var history actionHistorySnapshot
				if err := store.read("actions/rollback", &history); err != nil {
					t.Fatal(err)
				}
				e := history.Items[id]
				e.Apply = actionPhase{State: "not_attempted"}
				history.Items[id] = e
				if err := store.update("actions/rollback", history); err != nil {
					t.Fatal(err)
				}
			}
			if change != "orphan" {
				snapshot.Items[id] = ref
			}
			if err := store.updateMany(map[string]any{rollbackOutcomeNamespace + "rollback": snapshot, "future/opaque": map[string]int{"version": 9}}); err != nil {
				t.Fatal(err)
			}
			_, restored, err := newManagementServer(m.client, m.opts)
			valid := change == "preserve" || change == "n_minus_one" || change == "orphan"
			if valid != (err == nil) {
				t.Fatalf("restore %s: %v", change, err)
			}
			if !valid {
				return
			}
			if change == "orphan" && len(restored.rollbackOutcomes) != 0 {
				t.Fatal("orphan lookup retained")
			}
			if change == "n_minus_one" && restored.actions[id].Apply.State != "outcome_unknown" {
				t.Fatal("old history was not reconciled from original live attempted plan")
			}
			if err = restored.persistPlans(); err != nil {
				t.Fatal(err)
			}
			var opaque map[string]int
			if err = store.read("future/opaque", &opaque); err != nil || opaque["version"] != 9 {
				t.Fatal("future namespace lost")
			}
		})
	}
}

func TestRollbackOutcomeGrantPurge(t *testing.T) {
	m, id, _ := rollbackOutcomeFixture(t)
	store := m.opts.planStore
	saved := rollbackOutcomeSnapshot{Version: 1, Items: map[string]rollbackOutcomeLookup{id: m.rollbackOutcomes[id]}}
	if err := store.update(rollbackOutcomeNamespace+"other", saved); err != nil {
		t.Fatal(err)
	}
	if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{"other": {}}}); err != nil {
		t.Fatal(err)
	}
	var removed, retained rollbackOutcomeSnapshot
	if err := store.read(rollbackOutcomeNamespace+"rollback", &removed); err != nil {
		t.Fatal(err)
	}
	if err := store.read(rollbackOutcomeNamespace+"other", &retained); err != nil {
		t.Fatal(err)
	}
	if len(removed.Items) != 0 || len(retained.Items) != 1 {
		t.Fatal("grant purge retained revoked reference or removed another grant")
	}
}
