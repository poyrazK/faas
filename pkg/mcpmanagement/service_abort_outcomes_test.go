// adr: 20261104144000
package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func serviceAbortOutcomeFixture(t *testing.T) (*management, string, *api.ServiceAbortReceipt) {
	t.Helper()
	f, _, _ := newReleaseFixture(t, true)
	f.app.Manifest.ExecutionMode = api.ExecutionModeService
	f.latest = "release-new"
	f.deployments[f.latest] = api.DeploymentResponse{ID: f.latest, AppID: f.app.ID, Scope: "default", Status: "live", RolloutState: "rolling_out"}
	store, _, _ := testStateStore(t)
	r := &api.ServiceAbortReceipt{}
	var acceptedAttempt string
	var id string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.Contains(req.URL.Path, "/service-aborts/") {
			if req.Method != http.MethodGet || req.Header.Get(api.ServiceAbortAttemptHeader) != "" || req.Header.Get("Idempotency-Key") != "" || !strings.HasSuffix(req.URL.Path, acceptedAttempt) {
				t.Error("receipt lookup forwarded mutation metadata or changed identity")
			}
			_ = json.NewEncoder(w).Encode(r)
			return
		}
		if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/rollouts/recover") {
			if r.AttemptID != "" {
				t.Error("replayed original abort")
			}
			var body api.RecoverRolloutRequest
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			var refs serviceAbortOutcomeSnapshot
			var plans map[string]storedPlan
			if err := store.read(serviceAbortOutcomeNamespace+"abort", &refs); err != nil {
				t.Error(err)
			}
			if err := store.read("plans/abort", &plans); err != nil {
				t.Error(err)
			}
			ref := refs.Items[id]
			acceptedAttempt = ref.AttemptID
			if !plans[id].Attempted || !validServiceAbortOutcomeLookup(ref) || ref.AttemptID != req.Header.Get(api.ServiceAbortAttemptHeader) || ref.RequestHash != api.ServiceAbortRequestHash(f.app.ID, body) {
				t.Error("native POST preceded durable attempt")
			}
			*r = api.ServiceAbortReceipt{AttemptID: ref.AttemptID, RequestHash: ref.RequestHash, AppID: f.app.ID, DeploymentID: body.DeploymentID, PredecessorDeploymentID: body.ExpectedPredecessorDeploymentID, Scope: "default", RequestID: uuid.NewString(), AcceptedAt: time.Now().UTC()}
			api.WriteProblem(w, api.ErrCapacity("lost reply"))
			return
		}
		f.serveHTTP(w, req)
	}))
	t.Cleanup(backend.Close)
	_, m, err := newHistoryTestManagementServer(api.NewClient(backend.URL, testToken).SetCompletionCache(nil), Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, planStore: store, planNamespace: "abort"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := m.planServiceAbort(t.Context(), planServiceAbortArgs{App: "alpha", DeploymentID: f.latest, PredecessorDeploymentID: "release-base", Reason: "PRIVATE_ABORT_REASON"})
	if err != nil {
		t.Fatal(err)
	}
	id = v.(releasePlan).ID
	if _, err := m.applyServiceAbort(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("lost reply succeeded")
	}
	return m, id, r
}

func TestServiceAbortOutcomeLostReplyExpiry(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			m, id, receipt := serviceAbortOutcomeFixture(t)
			path := m.opts.planStore.persist.(*fileState).path
			key := filepath.Join(filepath.Dir(path), "key")
			ref := m.serviceAbortOutcomes[id]
			m.plans[id].ExpiresAt = time.Now().Add(-time.Minute)
			if err := m.persistPlans(); err != nil {
				t.Fatal(err)
			}
			if err := m.opts.planStore.Close(); err != nil {
				t.Fatal(err)
			}
			next, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			opts := m.opts
			opts.planStore, opts.AllowWrites = next, false
			cs, ctx := connectConfiguredTestServer(t, &httptest.Server{URL: opts.APIBase}, opts, protocol)
			before, _ := os.ReadFile(path)
			for _, secret := range []string{ref.AttemptID, ref.RequestHash, "PRIVATE_ABORT_REASON"} {
				if bytes.Contains(before, []byte(secret)) {
					t.Fatal("plaintext recovery reference")
				}
			}
			args := getActionArgs{App: "alpha", PlanID: id}
			history := callTool(t, ctx, cs, "gregale_get_action", args, false)
			var view actionView
			if json.Unmarshal(history["action"], &view) != nil || view.Apply.State != "outcome_unknown" || view.PlanAvailable || view.Recovery == nil || view.Recovery.Outcome == nil {
				t.Fatalf("history: %s", history)
			}
			resource, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: view.Recovery.ResourceURI})
			if err != nil {
				t.Fatal(err)
			}
			recipe := recoveryResourceView(t, resource, view.Recovery.ResourceURI)
			if recipe.Workflow.Outcome == nil || recipe.Workflow.Check.Tool != "" || recipe.Workflow.Review.Tool != "" {
				t.Fatal("expired recipe restored write authority")
			}
			if _, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_saved_plan_review", Arguments: map[string]string{"app": "alpha", "plan_id": id}}); err != nil {
				t.Fatal(err)
			}
			result := callTool(t, ctx, cs, "gregale_get_action_outcome", args, false)
			body, _ := json.Marshal(result)
			if string(result["outcome"]) != `"verified_receipt"` || string(result["native_completion"]) != `"not_observed"` || !bytes.Contains(result["receipt"], []byte(receipt.RequestID)) || bytes.Contains(body, []byte(ref.RequestHash)) || bytes.Contains(body, []byte(ref.AttemptID)) || bytes.Contains(body, []byte("PRIVATE_ABORT_REASON")) {
				t.Fatalf("outcome: %s", body)
			}
			var observation savedPlanReference
			if json.Unmarshal(result["observation"], &observation) != nil || observation.Tool != "gregale_get_rollout" {
				t.Fatal("missing exact handoff observation")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("read-only recovery changed durable state")
			}
		})
	}
}

func TestServiceAbortOutcomeEvidenceGuards(t *testing.T) {
	tests := map[string]func(*api.ServiceAbortReceipt){
		"attempt":         func(r *api.ServiceAbortReceipt) { r.AttemptID = strings.Repeat("f", 32) },
		"hash":            func(r *api.ServiceAbortReceipt) { r.RequestHash = strings.Repeat("f", 64) },
		"app":             func(r *api.ServiceAbortReceipt) { r.AppID = "other" },
		"candidate":       func(r *api.ServiceAbortReceipt) { r.DeploymentID = "other" },
		"predecessor":     func(r *api.ServiceAbortReceipt) { r.PredecessorDeploymentID = "other" },
		"scope":           func(r *api.ServiceAbortReceipt) { r.Scope = "staging" },
		"invalid_request": func(r *api.ServiceAbortReceipt) { r.RequestID = "not-a-native-uuid" },
		"request":         func(r *api.ServiceAbortReceipt) { r.RequestID = "" },
		"acceptance":      func(r *api.ServiceAbortReceipt) { r.AcceptedAt = time.Time{} },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			m, id, r := serviceAbortOutcomeFixture(t)
			change(r)
			v, err := m.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
			if err != nil {
				t.Fatal(err)
			}
			out := v.(*guardedRead).value.(actionOutcome)
			if out.Outcome != "unresolved" || out.Reason != "receipt_unverified" || out.Receipt != nil || out.Observation != nil {
				t.Fatalf("unverified evidence accepted: %+v", out)
			}
		})
	}
}

func TestServiceAbortOutcomeFinalGuards(t *testing.T) {
	for _, kind := range []string{"reference", "history", "retention", "storage", "consent"} {
		t.Run(kind, func(t *testing.T) {
			m, id, _ := serviceAbortOutcomeFixture(t)
			v, err := m.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "reference":
				delete(m.serviceAbortOutcomes, id)
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
func TestServiceAbortOutcomeSnapshotCompatibility(t *testing.T) {
	for _, change := range []string{"preserve", "n_minus_one", "version", "binding", "attempt", "orphan"} {
		t.Run(change, func(t *testing.T) {
			m, id, _ := serviceAbortOutcomeFixture(t)
			store := m.opts.planStore
			var snapshot serviceAbortOutcomeSnapshot
			if err := store.read(serviceAbortOutcomeNamespace+"abort", &snapshot); err != nil {
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
				if err := store.read(workloadActionsNamespace+"abort", &history); err != nil {
					t.Fatal(err)
				}
				e := history.Items[id]
				e.Apply = actionPhase{State: "not_attempted"}
				history.Items[id] = e
				if err := store.update(workloadActionsNamespace+"abort", history); err != nil {
					t.Fatal(err)
				}
			}
			if change != "orphan" {
				snapshot.Items[id] = ref
			}
			if err := store.updateMany(map[string]any{serviceAbortOutcomeNamespace + "abort": snapshot, "future/opaque": map[string]int{"version": 9}}); err != nil {
				t.Fatal(err)
			}
			_, restored, err := newHistoryTestManagementServer(m.client, m.opts)
			valid := change == "preserve" || change == "n_minus_one" || change == "orphan"
			if valid != (err == nil) {
				t.Fatalf("restore %s: %v", change, err)
			}
			if !valid {
				return
			}
			if change == "orphan" && len(restored.serviceAbortOutcomes) != 0 {
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

func TestServiceAbortOutcomeGrantPurge(t *testing.T) {
	m, id, _ := serviceAbortOutcomeFixture(t)
	store := m.opts.planStore
	saved := serviceAbortOutcomeSnapshot{Version: 1, Items: map[string]serviceAbortOutcomeLookup{id: m.serviceAbortOutcomes[id]}}
	if err := store.update(serviceAbortOutcomeNamespace+"other", saved); err != nil {
		t.Fatal(err)
	}
	if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{"other": {}}}); err != nil {
		t.Fatal(err)
	}
	var removed, retained serviceAbortOutcomeSnapshot
	if err := store.read(serviceAbortOutcomeNamespace+"abort", &removed); err != nil {
		t.Fatal(err)
	}
	if err := store.read(serviceAbortOutcomeNamespace+"other", &retained); err != nil {
		t.Fatal(err)
	}
	if len(removed.Items) != 0 || len(retained.Items) != 1 {
		t.Fatal("grant purge retained revoked reference or removed another grant")
	}
}

func TestServiceAbortOutcomeLegacyAndPruning(t *testing.T) {
	m, id, _ := serviceAbortOutcomeFixture(t)
	p := m.plans[id]
	delete(m.serviceAbortOutcomes, id)
	if _, err := m.prepareServiceAbortOutcome(t.Context(), p); err != nil || len(m.serviceAbortOutcomes) != 0 {
		t.Fatal("backfilled legacy attempt")
	}
	p.attempted = false
	p.Scope = "staging"
	if _, err := m.prepareServiceAbortOutcome(t.Context(), p); err != nil || len(m.serviceAbortOutcomes) != 0 {
		t.Fatal("minted named service abort identity")
	}
	m, id, _ = serviceAbortOutcomeFixture(t)
	ref := m.serviceAbortOutcomes[id]
	ref.RequestHash = strings.Repeat("f", 64)
	m.serviceAbortOutcomes[id] = ref
	if err := m.persistPlans(); err == nil {
		t.Fatal("accepted changed original request")
	}
	m, id, _ = serviceAbortOutcomeFixture(t)
	delete(m.plans, id)
	e := m.actions[id]
	e.RetainedUntil = time.Now().Add(-time.Second)
	m.actions[id] = e
	if err := m.persistPlans(); err != nil || len(m.serviceAbortOutcomes) != 0 {
		t.Fatalf("retained expired lookup: %v", err)
	}
}

func TestServiceAbortOutcomeAcceptedRequestIDGuard(t *testing.T) {
	m, id, r := serviceAbortOutcomeFixture(t)
	p := m.plans[id]
	p.result = &api.DeploymentResponse{ID: p.TargetDeploymentID, AppID: p.AppID, Scope: p.Scope, Status: "live", RolloutState: "rolling_out"}
	p.ServiceRecovery = &api.ServiceRolloutRecoveryReceipt{Status: "accepted", DeploymentID: p.TargetDeploymentID, PredecessorDeploymentID: p.PredecessorDeploymentID, RequestID: r.RequestID}
	if err := m.persistPlans(); err != nil {
		t.Fatal(err)
	}
	if m.actions[id].Apply.State != "accepted" {
		t.Fatal("missing original accepted reply")
	}
	r.RequestID = uuid.NewString()
	v, err := m.getActionOutcome(t.Context(), getActionArgs{App: "alpha", PlanID: id})
	if err != nil {
		t.Fatal(err)
	}
	out := v.(*guardedRead).value.(actionOutcome)
	if out.Outcome != "unresolved" || out.Reason != "receipt_unverified" {
		t.Fatalf("replaced original accepted request ID: %+v", out)
	}
}
