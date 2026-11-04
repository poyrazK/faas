// adr: 20261104173000
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

func namedServiceAbortOutcomeFixture(t *testing.T) (*management, string, *api.ServiceAbortReceipt, *namedStageFixture) {
	t.Helper()
	f, _, opts := namedStageOptions(t)
	f.mode, f.latest = api.ExecutionModeService, "stage-new"
	f.cache["seed"] = api.DeploymentResponse{ID: "stage-new", AppID: "app-one", Scope: "staging", Status: "live", RolloutState: "rolling_out"}
	store, _, _ := testStateStore(t)
	opts.planStore, opts.planNamespace = store, "named-abort"
	var id, attempt string
	receipt := &api.ServiceAbortReceipt{}
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/service-aborts/") {
			if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, attempt) || r.Header.Get("Idempotency-Key") != "" || r.Header.Get(api.ServiceAbortAttemptHeader) != "" {
				t.Error("receipt read changed original attempt or forwarded write metadata")
			}
			_ = json.NewEncoder(w).Encode(receipt)
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/rollouts/recover") {
			if attempt != "" {
				t.Error("replayed named abort")
			}
			var body api.RecoverRolloutRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			var refs serviceAbortOutcomeSnapshot
			var plans map[string]storedPlan
			if err := store.read(namedServiceAbortOutcomeNamespace+"named-abort", &refs); err != nil {
				t.Error(err)
			}
			if err := store.read("plans/named-abort", &plans); err != nil {
				t.Error(err)
			}
			ref := refs.Items[id]
			attempt = ref.AttemptID
			if !plans[id].Attempted || !validServiceAbortOutcomeLookup(ref) || attempt != r.Header.Get(api.ServiceAbortAttemptHeader) || ref.RequestHash != api.ServiceAbortRequestHash("app-one", body) {
				t.Error("named POST preceded atomic persistence")
			}
			*receipt = api.ServiceAbortReceipt{AttemptID: attempt, RequestHash: ref.RequestHash, AppID: "app-one", Scope: "staging", ProjectID: "project-one", EnvironmentID: f.envID, DeploymentID: body.DeploymentID, PredecessorDeploymentID: body.ExpectedPredecessorDeploymentID, RequestID: uuid.NewString(), AcceptedAt: time.Now().UTC()}
			api.WriteProblem(w, api.ErrCapacity("lost named abort reply"))
			return
		}
		f.serve(w, r)
	}))
	t.Cleanup(b.Close)
	opts.APIBase = b.URL
	_, m, err := newManagementServer(api.NewClient(b.URL, testToken).SetCompletionCache(nil), opts)
	if err != nil {
		t.Fatal(err)
	}
	v, err := m.planServiceAbort(t.Context(), planServiceAbortArgs{App: "alpha", Project: "shop", Environment: "staging", DeploymentID: "stage-new", PredecessorDeploymentID: "stage-base", Reason: "PRIVATE_NAMED_ABORT_REASON"})
	if err != nil {
		t.Fatal(err)
	}
	id = v.(releasePlan).ID
	if _, err := m.applyServiceAbort(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("lost reply succeeded")
	}
	return m, id, receipt, f
}

func TestServiceAbortNamedLostReplyExpiry(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			m, id, r, _ := namedServiceAbortOutcomeFixture(t)
			path := m.opts.planStore.path
			m.plans[id].ExpiresAt = time.Now().Add(-time.Minute)
			if err := m.persistPlans(); err != nil {
				t.Fatal(err)
			}
			if err := m.opts.planStore.Close(); err != nil {
				t.Fatal(err)
			}
			next, err := OpenStateStore(path, filepath.Join(filepath.Dir(path), "key"))
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			opts := m.opts
			opts.planStore, opts.AllowWrites = next, false
			cs, ctx := connectConfiguredTestServer(t, &httptest.Server{URL: opts.APIBase}, opts, protocol)
			before, _ := os.ReadFile(path)
			args := getActionArgs{App: "alpha", Project: "shop", Environment: "staging", PlanID: id}
			got := callTool(t, ctx, cs, "gregale_get_action", args, false)
			var view actionView
			if json.Unmarshal(got["action"], &view) != nil || view.PlanAvailable || view.Recovery == nil || view.Recovery.Outcome == nil || view.Apply.State != "outcome_unknown" {
				t.Fatalf("named discovery: %s", got)
			}
			resource, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: view.Recovery.ResourceURI})
			if err != nil {
				t.Fatal(err)
			}
			recipe := recoveryResourceView(t, resource, view.Recovery.ResourceURI)
			if recipe.Workflow.Outcome == nil || recipe.Workflow.Check.Tool != "" || recipe.Workflow.Review.Tool != "" {
				t.Fatal("expired named recipe renewed authority")
			}
			if _, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "gregale_saved_plan_review", Arguments: map[string]string{"app": "alpha", "project": "shop", "environment": "staging", "plan_id": id}}); err != nil {
				t.Fatal(err)
			}
			out := callTool(t, ctx, cs, "gregale_get_action_outcome", args, false)
			if string(out["outcome"]) != `"verified_receipt"` || string(out["native_completion"]) != `"not_observed"` || !bytes.Contains(out["receipt"], []byte(r.RequestID)) {
				t.Fatalf("named acceptance: %s", out)
			}
			var observation struct {
				Tool      string   `json:"tool"`
				Arguments waitArgs `json:"arguments"`
			}
			if json.Unmarshal(out["observation"], &observation) != nil || observation.Tool != "gregale_get_rollout" || observation.Arguments.Project != "shop" || observation.Arguments.Environment != "staging" || observation.Arguments.DeploymentID != "stage-new" {
				t.Fatalf("unscoped observation: %s", out["observation"])
			}
			body, _ := json.Marshal(out)
			if bytes.Contains(body, []byte(r.AttemptID)) || bytes.Contains(body, []byte(r.RequestHash)) || bytes.Contains(body, []byte("PRIVATE_NAMED_ABORT_REASON")) {
				t.Fatal("private lookup exposed")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("named recovery changed durable state")
			}
		})
	}
}

func TestServiceAbortNamedIdentityAndConsent(t *testing.T) {
	for _, kind := range []string{"project_receipt", "environment_receipt", "scope_receipt", "environment_replaced", "read_revoked", "final_read_revoked", "final_environment_replaced", "protected_read", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			m, id, r, f := namedServiceAbortOutcomeFixture(t)
			args := getActionArgs{App: "alpha", Project: "shop", Environment: "staging", PlanID: id}
			switch kind {
			case "project_receipt":
				r.ProjectID = "other"
			case "environment_receipt":
				r.EnvironmentID = "other"
			case "scope_receipt":
				r.Scope = "default"
			case "environment_replaced":
				f.mu.Lock()
				f.envID = "replacement"
				f.mu.Unlock()
			case "read_revoked":
				delete(m.opts.Environments.Named, "shop/staging")
			case "protected_read":
				f.mu.Lock()
				f.protected = true
				f.mu.Unlock()
			case "legacy":
				delete(m.serviceAbortOutcomes, id)
				if _, err := m.prepareServiceAbortOutcome(t.Context(), m.plans[id]); err != nil || len(m.serviceAbortOutcomes) != 0 {
					t.Fatal("backfilled named legacy attempt")
				}
			}
			v, err := m.getActionOutcome(t.Context(), args)
			if kind == "read_revoked" || kind == "environment_replaced" {
				if err == nil {
					t.Fatal("invalid target released evidence")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			read := v.(*guardedRead)
			out := read.value.(actionOutcome)
			switch kind {
			case "final_read_revoked":
				delete(m.opts.Environments.Named, "shop/staging")
				if read.check(t.Context()) == nil {
					t.Fatal("revoked final consent released evidence")
				}
			case "final_environment_replaced":
				f.mu.Lock()
				f.envID = "replacement"
				f.mu.Unlock()
				if read.check(t.Context()) == nil {
					t.Fatal("replaced final environment released evidence")
				}
			case "protected_read":
				if out.Outcome != "verified_receipt" {
					t.Fatal("protection change erased historical acceptance")
				}
			case "legacy":
				if out.Outcome != "unresolved" || out.Reason != "lookup_unavailable" {
					t.Fatal("invented legacy evidence")
				}
			default:
				if out.Outcome != "unresolved" || out.Reason != "receipt_unverified" {
					t.Fatalf("wrong scope verified: %+v", out)
				}
			}
		})
	}
}
