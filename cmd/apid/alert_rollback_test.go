package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAlertRollbackRestartsAndRequiresExactPredecessorEvidence(t *testing.T) {
	e, app, predecessor, candidate := promotionFixture(t)
	ctx := context.Background()
	if _, err := e.store.UpdateDeploymentTraffic(ctx, candidate.ID, 25); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentCanaryState(ctx, candidate.ID, "balanced", 1, 4, time.Now().Add(-time.Minute), "rolling_out"); err != nil {
		t.Fatal(err)
	}
	rule, err := e.store.CreateAlertRule(ctx, state.AlertRule{AccountID: e.acct.ID, AppID: app.ID, Name: "rollback-alert", Enabled: true, Metric: state.AlertMetricErrorRate, Comparison: state.AlertGt, Threshold: 1, WindowSpec: state.AlertWindow5m, Action: state.AlertActionRollback, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("unavailable-secret"), CooldownMinutes: 15})
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := e.store.SetBindingReleasePolicy(ctx, e.acct.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	fire, won, err := e.store.ClaimAlertFire(ctx, rule.ID, rule.ID+":fire", nil, 42, time.Now())
	if err != nil || !won {
		t.Fatalf("fire %v %v", won, err)
	}
	// No meterd callback or webhook delivery occurs. The durable fire is enough.
	if err := e.s.alertRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	blocked, err := e.store.ReadAlertRollback(ctx, fire)
	if err != nil || blocked.Status != "blocked" || len(blocked.Blockers) == 0 || blocked.CandidateDeploymentID != candidate.ID || blocked.PredecessorDeploymentID != predecessor.ID {
		t.Fatalf("blockers %+v %v", blocked, err)
	}
	assertPromotionWeights(t, e, predecessor, candidate, 25)
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	if err := e.s.alertRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	still, _ := e.store.ReadAlertRollback(ctx, fire)
	if still.Status != "blocked" {
		t.Fatalf("candidate evidence authorized recipient %+v", still)
	}
	completePromotionProbe(t, e, app, predecessor, passedPostgresVerification)
	old := e.s
	e.s = newServer(e.store, old.log, "gregale.dev", noopNotifier{}).WithManagedPostgres(old.managedPostgres, nil, old.managedPostgresBindings, nil, nil, nil)
	if err := e.s.alertRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	complete, err := e.store.ReadAlertRollback(ctx, fire)
	if err != nil || complete.Status != "complete" || complete.CompletedAt == nil || complete.AuditID == "" {
		t.Fatalf("restart %+v %v", complete, err)
	}
	assertPromotionWeights(t, e, predecessor, candidate, 0)
	const token = "alert-rollback-action-token-00000000001"
	mux := http.NewServeMux()
	if err := e.s.mountInternalSafeDeploy(mux, "127.0.0.1:9101", "alert-rollback-canary-token-00000000001", token); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		r := httptest.NewRequest(http.MethodPost, "/v1/internal/safe-deploy/alert-rollbacks/"+fire, nil)
		r.RemoteAddr = "127.0.0.1:9000"
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var replay api.AlertRollback
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &replay) != nil || replay.AuditID != complete.AuditID {
			t.Fatalf("duplicate callback %d %s", w.Code, w.Body.String())
		}
	}
	audits, err := e.store.ListDeploymentAuditByAlertRule(ctx, rule.ID, 100)
	if err != nil || len(audits) != 1 {
		t.Fatalf("duplicate audits %+v %v", audits, err)
	}
	// GET status remains a read and uses both app and account ownership.
	path := "/v1/apps/" + app.Slug + "/alert-rollbacks/" + fire
	response := e.do(t, http.MethodGet, path, nil, nil)
	if response.Code != 200 {
		t.Fatalf("owned status %d %s", response.Code, response.Body.String())
	}
	response = e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/alert-rollbacks", nil, nil)
	if response.Code != 200 {
		t.Fatalf("owned list %d %s", response.Code, response.Body.String())
	}
	other, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "other-" + uuid.NewString()[:8], RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	response = e.do(t, http.MethodGet, "/v1/apps/"+other.Slug+"/alert-rollbacks/"+fire, nil, nil)
	if response.Code != 404 {
		t.Fatalf("wrong app leaked status %d %s", response.Code, response.Body.String())
	}
}

func TestAlertRollbackChangedCanaryFailsBeforeBindingChecks(t *testing.T) {
	e, app, _, candidate := promotionFixture(t)
	ctx := context.Background()
	if _, err := e.store.UpdateDeploymentTraffic(ctx, candidate.ID, 25); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentCanaryState(ctx, candidate.ID, "balanced", 1, 4, time.Now(), "rolling_out"); err != nil {
		t.Fatal(err)
	}
	rule, err := e.store.CreateAlertRule(ctx, state.AlertRule{AccountID: e.acct.ID, AppID: app.ID, Name: "changed-alert", Enabled: true, Metric: state.AlertMetricErrorRate, Comparison: state.AlertGt, Threshold: 1, WindowSpec: state.AlertWindow5m, Action: state.AlertActionRollback, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("sealed"), CooldownMinutes: 15})
	if err != nil {
		t.Fatal(err)
	}
	fire, _, err := e.store.ClaimAlertFire(ctx, rule.ID, rule.ID+":fire", nil, 42, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentCanaryState(ctx, candidate.ID, "balanced", 4, 4, time.Now(), "complete"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.alertRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := e.store.ReadAlertRollback(ctx, fire)
	if err != nil || r.Status != "failed" || r.Code != "alert_rollback_deployment_changed" {
		t.Fatalf("changed pair %+v %v", r, err)
	}
}
