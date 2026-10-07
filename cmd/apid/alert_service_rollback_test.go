package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAlertServiceRollbackResumesExactHandoffAndWaitsForDrain(t *testing.T) {
	e, app, predecessor, candidate := promotionFixture(t)
	ctx := t.Context()
	manifest := app.Manifest
	manifest.ExecutionMode = api.ExecutionModeService
	var err error
	app, err = e.store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentCanaryState(ctx, candidate.ID, "none", 0, 0, time.Now(), "rolling_out"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.BeginServiceRolloutCutover(ctx, candidate.ID); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := e.store.SetBindingReleasePolicy(ctx, e.acct.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	rule, err := e.store.CreateAlertRule(ctx, state.AlertRule{AccountID: e.acct.ID, AppID: app.ID, Name: "service-alert", Enabled: true, Metric: state.AlertMetricErrorRate, Comparison: state.AlertGt, Threshold: 1, WindowSpec: state.AlertWindow5m, Action: state.AlertActionRollback, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("unavailable-secret"), CooldownMinutes: 15})
	if err != nil {
		t.Fatal(err)
	}
	fire, won, err := e.store.ClaimAlertFire(ctx, rule.ID, rule.ID+":fire", nil, 42, time.Now())
	if err != nil || !won {
		t.Fatalf("fire %v %v", won, err)
	}
	read := func() api.AlertRollback {
		t.Helper()
		response := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/alert-rollbacks/"+fire, nil, nil)
		var r api.AlertRollback
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &r) != nil || !r.Service || r.CandidateDeploymentID != candidate.ID || r.PredecessorDeploymentID != predecessor.ID {
			t.Fatalf("service status %d %s", response.Code, response.Body)
		}
		return r
	}
	if err := e.s.alertRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	requested := read()
	if requested.ServiceRequestID != requested.ID || requested.Status != "pending" || requested.CompletedAt != nil {
		t.Fatalf("not accepted durable intent %+v", requested)
	}
	cursor := 0
	if err := e.s.serviceRolloutBindingSweep(ctx, &cursor); err != nil {
		t.Fatal(err)
	}
	if err := e.s.alertRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	if blocked := read(); blocked.Status != "blocked" || len(blocked.Blockers) == 0 {
		t.Fatalf("missing binding blockers %+v", blocked)
	}
	completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
	if err := e.s.serviceRolloutBindingSweep(ctx, &cursor); err != nil {
		t.Fatal(err)
	}
	assertPromotionWeights(t, e, predecessor, candidate, 100)
	completePromotionProbe(t, e, app, predecessor, passedPostgresVerification)
	if err := e.s.serviceRolloutBindingSweep(ctx, &cursor); err != nil {
		t.Fatal(err)
	}
	if err := e.s.alertRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	if blocked := read(); blocked.Status != "blocked" || blocked.Code != "service_rollout_not_ready" {
		t.Fatalf("missing capacity blockers %+v", blocked)
	}
	if _, err := e.store.CreateInstanceWithMode(ctx, app.ID, predecessor.ID, "running", 512, state.DefaultLocalNodeName, uuid.NewString(), "service"); err != nil {
		t.Fatal(err)
	}
	old := e.s
	e.s = newServer(e.store, old.log, "gregale.dev", noopNotifier{}).WithManagedPostgres(old.managedPostgres, nil, old.managedPostgresBindings, nil, nil, nil)
	if err := e.s.serviceRolloutBindingSweep(ctx, &cursor); err != nil {
		t.Fatal(err)
	}
	if err := e.s.alertRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	assertPromotionWeights(t, e, predecessor, candidate, 0)
	if routed := read(); routed.Status != "pending" || routed.ServicePhase != "routing" || routed.CompletedAt != nil {
		t.Fatalf("routing mistaken for completion %+v", routed)
	}
	d, err := e.store.DeploymentByID(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	h := d.ServiceRolloutHandoff
	now := time.Now().UTC()
	h.Phase, h.AcknowledgedAt = "draining", &now
	if _, err := e.store.UpdateServiceRolloutHandoff(ctx, candidate.ID, h); err != nil {
		t.Fatal(err)
	}
	if err := e.s.alertRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	if draining := read(); draining.Status != "pending" || draining.ServicePhase != "draining" {
		t.Fatalf("draining mistaken for completion %+v", draining)
	}
	if _, err := e.store.AbortServiceRollout(ctx, candidate.ID, requested.Reason); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := e.s.alertRollbackSweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	complete := read()
	if complete.Status != "complete" || complete.CompletedAt == nil || complete.ServicePhase != "complete" || complete.ServiceRoutingAuditID == "" || complete.AuditID == requested.AuditID {
		t.Fatalf("completion receipt %+v", complete)
	}
	audits, err := e.store.ListDeploymentAuditByAlertRule(ctx, rule.ID, 100)
	if err != nil || len(audits) != 2 {
		t.Fatalf("duplicate fire audits %+v %v", audits, err)
	}
}
