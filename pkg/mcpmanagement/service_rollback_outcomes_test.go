// adr: 20261105082446
package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceRollbackOutcomeLostReplyExpiry(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f, _, _ := newReleaseFixture(t, true)
			f.app.Manifest.ExecutionMode = api.ExecutionModeService
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
					if err := store.read(serviceRollbackOutcomeNamespace+"rollback", &refs); err != nil {
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
					receipt.Store(&api.RollbackOperation{ID: uuid.MustParse(ref.AttemptID).String(), AppID: "app-one", Scope: "default", Service: true, TargetDeploymentID: *req.TargetDeploymentID, CurrentDeploymentID: *req.ExpectedCurrentDeploymentID, Reason: req.Reason, Status: "preparing", CreatedAt: time.Now().UTC()})
					api.WriteProblem(w, api.ErrCapacity("lost reply"))
					return
				}
				f.serveHTTP(w, r)
			}))
			defer backend.Close()
			opts := Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "rollback"}
			_, m, err := newHistoryTestManagementServer(api.NewClient(backend.URL, testToken), opts)
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
			ref := m.serviceRollbackOutcomes[planID]
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
			var rollout, health savedPlanReference
			if json.Unmarshal(result["rollout_observation"], &rollout) != nil || rollout.Tool != "gregale_get_rollout" || json.Unmarshal(result["health_observation"], &health) != nil || health.Tool != "gregale_get_health" {
				t.Fatalf("missing independent service observations: %s", body)
			}
			var rolloutArgs actionObservationArgs
			raw, _ := json.Marshal(rollout.Arguments)
			if json.Unmarshal(raw, &rolloutArgs) != nil || rolloutArgs.App != "alpha" || rolloutArgs.DeploymentID != ref.TargetID || rolloutArgs.Environment != "" {
				t.Fatal("lost exact service rollout identity")
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

func serviceRollbackOutcomeFixture(t *testing.T) (*management, string, *api.RollbackOperation) {
	t.Helper()
	f, _, _ := newReleaseFixture(t, true)
	f.app.Manifest.ExecutionMode = api.ExecutionModeService
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
	_, m, err := newHistoryTestManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "rollback"})
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
	*op = api.RollbackOperation{ID: uuid.MustParse(m.serviceRollbackOutcomes[id].AttemptID).String(), AppID: p.AppID, Scope: p.Scope, Service: true, TargetDeploymentID: p.TargetDeploymentID, CurrentDeploymentID: p.ExpectedCurrentID, Reason: p.Reason, Status: "preparing", CreatedAt: time.Now().UTC()}
	return m, id, op
}

func TestServiceRollbackOutcomeEvidenceAndFinalGuards(t *testing.T) {
	for name, change := range map[string]func(*api.RollbackOperation){
		"id": func(r *api.RollbackOperation) { r.ID = "wrong" }, "owner": func(r *api.RollbackOperation) { r.AppID = "foreign" },
		"scope": func(r *api.RollbackOperation) { r.Scope = "staging" }, "target": func(r *api.RollbackOperation) { r.TargetDeploymentID = "other" },
		"current": func(r *api.RollbackOperation) { r.CurrentDeploymentID = "other" }, "request": func(r *api.RollbackOperation) { r.Service = false },
		"reason": func(r *api.RollbackOperation) { r.Reason = "changed" }, "trigger": func(r *api.RollbackOperation) { r.Trigger = "manual" },
		"time": func(r *api.RollbackOperation) { r.CreatedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			m, id, r := serviceRollbackOutcomeFixture(t)
			change(r)
			v, err := m.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
			if err != nil {
				t.Fatal(err)
			}
			out := v.(*guardedRead).value.(actionOutcome)
			if out.Outcome != "unresolved" || out.Reason != "receipt_unverified" || out.Receipt != nil || out.Observation != nil || out.RolloutObservation != nil || out.HealthObservation != nil {
				t.Fatalf("unverified service receipt released: %+v", out)
			}
		})
	}
	for _, kind := range []string{"reference", "collision", "history", "retention", "storage", "consent"} {
		t.Run("final_"+kind, func(t *testing.T) {
			m, id, _ := serviceRollbackOutcomeFixture(t)
			v, err := m.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "reference":
				delete(m.serviceRollbackOutcomes, id)
			case "collision":
				m.rollbackOutcomes = map[string]rollbackOutcomeLookup{id: m.serviceRollbackOutcomes[id]}
			case "history":
				delete(m.actions, id)
			case "retention":
				e := m.actions[id]
				e.RetainedUntil = time.Now().Add(-time.Minute)
				m.actions[id] = e
			case "storage":
				_ = m.opts.planStore.Close()
			case "consent":
				m.opts.AllowedApps = map[string]string{"other": "other"}
			}
			if v.(*guardedRead).check(t.Context()) == nil {
				t.Fatal("released changed service recovery guard")
			}
		})
	}
}

func TestServiceRollbackOutcomeNamespaceAndLegacy(t *testing.T) {
	for _, kind := range []string{"preserve", "version", "binding", "attempt", "wrong_namespace", "orphan"} {
		t.Run(kind, func(t *testing.T) {
			m, id, _ := serviceRollbackOutcomeFixture(t)
			store := m.opts.planStore
			var saved rollbackOutcomeSnapshot
			if err := store.read(serviceRollbackOutcomeNamespace+"rollback", &saved); err != nil {
				t.Fatal(err)
			}
			ref := saved.Items[id]
			switch kind {
			case "version":
				saved.Version = 2
			case "binding":
				ref.Binding = strings.Repeat("f", 64)
				saved.Items[id] = ref
			case "attempt":
				ref.AttemptID = "bad"
				saved.Items[id] = ref
			case "wrong_namespace":
				if err := store.update(rollbackOutcomeNamespace+"rollback", saved); err != nil {
					t.Fatal(err)
				}
			case "orphan":
				delete(saved.Items, id)
				saved.Items[strings.Repeat("f", 64)] = ref
			}
			if err := store.updateMany(map[string]any{serviceRollbackOutcomeNamespace + "rollback": saved, "future/opaque": map[string]int{"version": 9}}); err != nil {
				t.Fatal(err)
			}
			if kind == "preserve" { // N-1 rewrites its known request namespace only.
				if err := store.update(rollbackOutcomeNamespace+"rollback", rollbackOutcomeSnapshot{Version: 1, Items: map[string]rollbackOutcomeLookup{}}); err != nil {
					t.Fatal(err)
				}
			}
			opts := m.opts
			opts.AllowWrites = false
			_, restored, err := newHistoryTestManagementServer(m.client, opts)
			valid := kind == "preserve" || kind == "orphan"
			if valid != (err == nil) {
				t.Fatalf("restore %s: %v", kind, err)
			}
			if !valid {
				return
			}
			if kind == "preserve" {
				result, err := restored.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
				if err != nil || result.(*guardedRead).value.(actionOutcome).Outcome != "verified_receipt" {
					t.Fatalf("lost service evidence after N-1 rewrite: %v", err)
				}
			} else if len(restored.serviceRollbackOutcomes) != 0 {
				t.Fatal("orphan reference retained")
			}
			if err := restored.persistPlans(); err != nil {
				t.Fatal(err)
			}
			var opaque map[string]int
			if err := store.read("future/opaque", &opaque); err != nil || opaque["version"] != 9 {
				t.Fatal("unknown namespace lost")
			}
		})
	}
	m, id, _ := serviceRollbackOutcomeFixture(t)
	p := m.plans[id]
	delete(m.serviceRollbackOutcomes, id)
	if _, err := m.prepareRollbackOutcome(t.Context(), p); err != nil || len(m.serviceRollbackOutcomes) != 0 {
		t.Fatal("legacy attempt received replacement identity")
	}
	v, err := m.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
	if err != nil || v.(*guardedRead).value.(actionOutcome).Reason != "lookup_unavailable" {
		t.Fatal("legacy attempt fabricated evidence")
	}
	for _, mode := range []string{api.ExecutionModeWorker, api.ExecutionModeService} {
		p.attempted = false
		p.RolloutMode = mode
		p.Scope = "staging"
		if _, err := m.prepareRollbackOutcome(t.Context(), p); err != nil || len(m.serviceRollbackOutcomes) != 0 {
			t.Fatal("unsupported scope gained service lookup")
		}
	}
	m, id, _ = serviceRollbackOutcomeFixture(t)
	delete(m.plans, id)
	e := m.actions[id]
	e.RetainedUntil = time.Now().Add(-time.Minute)
	m.actions[id] = e
	if err := m.persistPlans(); err != nil || len(m.serviceRollbackOutcomes) != 0 {
		t.Fatal("expired service reference retained")
	}
}

func TestServiceRollbackOutcomeGrantPurge(t *testing.T) {
	m, id, _ := serviceRollbackOutcomeFixture(t)
	store := m.opts.planStore
	saved := rollbackOutcomeSnapshot{Version: 1, Items: map[string]rollbackOutcomeLookup{id: m.serviceRollbackOutcomes[id]}}
	if err := store.update(serviceRollbackOutcomeNamespace+"other", saved); err != nil {
		t.Fatal(err)
	}
	if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{"other": {}}}); err != nil {
		t.Fatal(err)
	}
	var removed, retained rollbackOutcomeSnapshot
	if err := store.read(serviceRollbackOutcomeNamespace+"rollback", &removed); err != nil {
		t.Fatal(err)
	}
	if err := store.read(serviceRollbackOutcomeNamespace+"other", &retained); err != nil {
		t.Fatal(err)
	}
	if len(removed.Items) != 0 || len(retained.Items) != 1 {
		t.Fatal("service grant purge crossed namespace or retained revoked evidence")
	}
}
