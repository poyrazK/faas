// handlers_rollouts_test.go — apid handler tests for the
// operator manual-rollout-recovery endpoint (issue #976 /
// ADR-122 / SAFE-RELEASES-R).
//
// Coverage matrix:
//
//   - happy path: recover --action=promote on a healthy
//     rollout short-circuits to traffic_percent=100 /
//     rollout_state=complete + audit row.
//   - happy path: recover --action=abort flips state=aborted
//     + emits deploy.rolled_back audit row.
//   - plan gate: Hobby / Free operators get 403
//     plan_traffic_split_not_allowed BEFORE the store is touched.
//   - shape errors: unknown action → 422
//     invalid_recover_action.
//   - not-found: no active rollout row → 404 (loadApp handles
//     it).
//
// Tests run KVM-free via the in-memory store; the
// store.RecoverRollout MemStore impl is the test seam so both
// backends share the same closed-set guards + audit emit.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// stampCanaryInMemStore hand-edits the MemStore's in-memory
// deployment row to simulate a canary rollout mid-flight via
// the SetDeploymentCanaryState test seam (the production
// writer is meterd's orchestrator; the handler tests need a
// deterministic starting state without driving the orchestrator).
func stampCanaryInMemStore(t *testing.T, e testEnv, depID string, rolloutState string, canaryStep, canaryTotalSteps int, stepStartedAt time.Time) {
	t.Helper()
	if err := e.store.SetDeploymentCanaryState(context.Background(), depID,
		"1-10-50-100", canaryStep, canaryTotalSteps, stepStartedAt, rolloutState); err != nil {
		t.Fatal(err)
	}
}

// seedAppWithRollout creates an app + deployment + stamps a
// canary rollout mid-flight. Returns the deployment id.
func seedAppWithRollout(t *testing.T, e testEnv, slug string, rolloutState string, canaryStep, canaryTotalSteps int, stepStartedAt time.Time) string {
	t.Helper()
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID,
		Slug:      slug,
		RAMMB:     256,
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		ImageDigest: "deadbeef",
		Status:      state.DeployLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	stampCanaryInMemStore(t, e, dep.ID, rolloutState, canaryStep, canaryTotalSteps, stepStartedAt)
	return dep.ID
}

// TestRecoverRollout_HappyPath_Promote exercises the promote
// short-circuit on a healthy rollout: traffic_percent=100 +
// rollout_state=complete + audit row with action='promote'.
func TestRecoverRollout_HappyPath_Promote(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedAppWithRollout(t, e, "happy", "rolling_out", 1, 4, time.Now().Add(-1*time.Minute))

	rec := e.do(t, "POST", "/v1/apps/happy/rollouts/recover",
		api.RecoverRolloutRequest{Action: "promote", Reason: "manual-test"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var out api.RolloutTransitionResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Deployment.RolloutState != "complete" {
		t.Errorf("rollout_state=%q, want complete", out.Deployment.RolloutState)
	}
	if out.AuditID == "" {
		t.Errorf("audit_id empty, want non-empty")
	}
}

// TestRecoverRollout_HappyPath_Abort exercises the abort path:
// rollout_state=aborted + audit row kind='deploy.rolled_back'.
func TestRecoverRollout_HappyPath_Abort(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedAppWithRollout(t, e, "abrt", "rolling_out", 2, 4, time.Now().Add(-2*time.Hour))

	rec := e.do(t, "POST", "/v1/apps/abrt/rollouts/recover",
		api.RecoverRolloutRequest{Action: "abort", Reason: "customer-confirmed-broken"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var out api.RolloutTransitionResponse
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Deployment.RolloutState != "aborted" {
		t.Errorf("rollout_state=%q, want aborted", out.Deployment.RolloutState)
	}
	if out.Deployment.RolloutAbortedReason != "customer-confirmed-broken" {
		t.Errorf("aborted_reason=%q, want %q", out.Deployment.RolloutAbortedReason, "customer-confirmed-broken")
	}
}

// TestRecoverRollout_PlanGate: rollout recovery is available on every plan
// as of ADR-199. This test inverts: Free and Hobby used to be refused 403
// plan_traffic_split_not_allowed before the store was touched, and must now
// get past the plan gate and be judged on the request itself.
//
// A Free operator whose canary wedged needs the recovery escape hatch more
// than a Scale operator does, not less — refusing it was the sharpest edge
// of the old gate.
func TestRecoverRollout_PlanGate(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby} {
		t.Run(string(plan), func(t *testing.T) {
			e := setup(t, plan)
			// A real rollout on a real app: the request must be judged
			// on its merits, not on the account's plan.
			seedAppWithRollout(t, e, "recov", "rolling_out", 1, 4, time.Now().Add(-2*time.Hour))
			rec := e.do(t, "POST", "/v1/apps/recov/rollouts/recover",
				api.RecoverRolloutRequest{Action: "abort", Reason: "plan-gate-regression-guard"}, nil)
			if rec.Code == http.StatusForbidden {
				t.Fatalf("plan=%s: rollout recovery refused 403; ADR-199 opened it to every plan. body %s",
					plan, rec.Body.String())
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("plan=%s: status %d, want 200; body %s", plan, rec.Code, rec.Body.String())
			}
			var out api.RolloutTransitionResponse
			if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
			if out.Deployment.RolloutState != "aborted" {
				t.Errorf("plan=%s: rollout_state=%q, want aborted", plan, out.Deployment.RolloutState)
			}
		})
	}
}

// TestRecoverRollout_InvalidAction: closed-set guard returns
// 422 invalid_recover_action BEFORE the store is touched.
func TestRecoverRollout_InvalidAction(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps/foo/rollouts/recover",
		api.RecoverRolloutRequest{Action: "explode"}, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
}

// TestRecoverRollout_NoActiveRollout: a Pro customer with no
// active rollout gets 404 (no active row to recover).
func TestRecoverRollout_NoActiveRollout(t *testing.T) {
	e := setup(t, api.PlanPro)
	if _, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID,
		Slug:      "norollout",
		RAMMB:     256,
		Status:    state.AppActive,
	}); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "POST", "/v1/apps/norollout/rollouts/recover",
		api.RecoverRolloutRequest{Action: "promote"}, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
}
