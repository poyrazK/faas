package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAlertHistoricalRollbackBlocksUnavailableTelemetryAndResumes(t *testing.T) {
	e, app, target, former := promotionFixture(t)
	ctx := t.Context()
	if err := e.store.MarkDeploymentLive(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateDeploymentStatus(ctx, former.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	current := seedBindingCandidate(t, e, app, "default")
	if _, err := e.store.UpdateDeploymentTraffic(ctx, current.ID, 100); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, current.ID); err != nil {
		t.Fatal(err)
	}
	e.s = e.s.WithRollbackArtifactVerifier(stubRollbackArtifactVerifier{})
	rule, err := e.store.CreateAlertRule(ctx, state.AlertRule{AccountID: e.acct.ID, AppID: app.ID, Name: "post-release", Enabled: true, Metric: state.AlertMetricErrorRate, Comparison: state.AlertGt, Threshold: 1, WindowSpec: state.AlertWindow5m, Action: state.AlertActionRollback, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("sealed"), CooldownMinutes: 15, PostDeployRollbackWindowSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}
	fire, won, err := e.store.ClaimAlertFire(ctx, rule.ID, rule.ID+":fire", nil, 42, time.Now().UTC())
	if err != nil || !won {
		t.Fatalf("fire %v %v", won, err)
	}
	read := func() api.AlertRollback {
		t.Helper()
		response := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/alert-rollbacks/"+fire, nil, nil)
		var r api.AlertRollback
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &r) != nil || !r.Historical || r.CandidateDeploymentID != current.ID || r.PredecessorDeploymentID != target.ID {
			t.Fatalf("status %d %s", response.Code, response.Body)
		}
		return r
	}
	e.s.rollbackArtifactVerifier = stubRollbackArtifactVerifier{err: state.ErrNotFound}
	if err := e.s.alertRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	if blocked := read(); blocked.Status != "blocked" || blocked.RollbackOperationID != "" {
		t.Fatalf("artifact outage became terminal or queued intent: %+v", blocked)
	}
	e.s.rollbackArtifactVerifier = stubRollbackArtifactVerifier{}
	if err := e.s.alertRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	blocked := read()
	if blocked.Status != "blocked" || blocked.Code != "alert_rollback_telemetry_unavailable" || blocked.RollbackOperationID != "" || blocked.DeploymentEvidence == nil {
		t.Fatalf("unqualified intent %+v", blocked)
	}
	assertPromotionWeights(t, e, target, current, 100)
	old := e.s
	e.s = newServer(e.store, old.log, "gregale.dev", noopNotifier{}).WithRollbackArtifactVerifier(stubRollbackArtifactVerifier{})
	if err := e.s.alertRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	retry := read()
	if retry.Status != "blocked" || retry.Code != blocked.Code || retry.DeploymentEvidence.WindowStart != blocked.DeploymentEvidence.WindowStart || retry.DeploymentEvidence.WindowEnd != blocked.DeploymentEvidence.WindowEnd {
		t.Fatalf("restart changed evidence selection %+v", retry)
	}
	audits, err := e.store.ListDeploymentAuditByAlertRule(ctx, rule.ID, 100)
	if err != nil || len(audits) != 0 {
		t.Fatalf("unqualified intent audited %+v %v", audits, err)
	}

}
func TestAlertPostDeployWindowValidationAndMerge(t *testing.T) {
	for _, n := range []int{-1, api.AlertRollbackMaxWindowSeconds + 1} {
		if validateAlertRollbackWindow(n, "rollback") == nil {
			t.Errorf("accepted %d", n)
		}
	}
	if validateAlertRollbackWindow(60, "webhook") == nil {
		t.Fatal("window accepted without rollback")
	}
	existing := state.AlertRule{Name: "rollback", Action: state.AlertActionRollback, PostDeployRollbackWindowSeconds: 600}
	name := "renamed"
	merged := alertRuleRowForValidation(existing, api.UpdateAlertRuleRequest{Name: &name})
	if merged.PostDeployRollbackWindowSeconds != 600 {
		t.Fatal("partial update cleared window")
	}
	zero := 0
	merged = alertRuleRowForValidation(existing, api.UpdateAlertRuleRequest{PostDeployRollbackWindowSeconds: &zero})
	if merged.PostDeployRollbackWindowSeconds != 0 {
		t.Fatal("explicit disable ignored")
	}
	response := alertRuleResponse(existing)
	if response.PostDeployRollbackWindowSeconds != 600 {
		t.Fatal("window missing from read")
	}
}

func TestHistoricalAlertDeadlinePrecedesArtifactReads(t *testing.T) {
	for _, missing := range []bool{false, true} {
		r := api.AlertRollback{Historical: true, FiredAt: time.Now().Add(-api.AlertRollbackEvidenceMaxCheckDelay)}
		want := "alert_rollback_evidence_expired"
		if missing {
			want = "alert_rollback_evidence_missing"
		} else {
			r.DeploymentEvidence = &api.AlertRollbackDeploymentEvidence{Version: 1}
		}
		// No store or artifact verifier is available. A spent or legacy fire
		// must terminate before either dependency is touched.
		status, code, _, err := (&server{}).queueHistoricalAlertRollback(t.Context(), r)
		if err != nil || status != "failed" || code != want {
			t.Fatalf("deadline %s %s %v", status, code, err)
		}
	}
}
