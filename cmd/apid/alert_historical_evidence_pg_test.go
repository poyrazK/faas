//go:build !no_pg

package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestHistoricalAlertEvidenceWorkerPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	ctx := t.Context()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "evidence-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	deployments := []state.Deployment{}
	for range 2 {
		d, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("1", 64), Scope: "default", Status: state.DeployPending, TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		if err = e.store.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err = e.store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		deployments = append(deployments, d)
	}
	target, current := deployments[0], deployments[1]
	cutover := time.Now().UTC().Truncate(time.Minute).Add(-5*time.Minute + 10*time.Second)
	if _, err = e.pool.Exec(ctx, "UPDATE deployments SET rollout_completed_at=$2 WHERE id=$1", current.ID, cutover); err != nil {
		t.Fatal(err)
	}
	rule, err := e.store.CreateAlertRule(ctx, state.AlertRule{AccountID: e.acct.ID, AppID: app.ID, Name: "evidence", Enabled: true, Metric: state.AlertMetricErrorRate, Comparison: state.AlertGt, Threshold: 1, WindowSpec: state.AlertWindow5m, Action: state.AlertActionRollback, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("sealed"), CooldownMinutes: 15, PostDeployRollbackWindowSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}
	fire, won, err := e.store.ClaimAlertFire(ctx, rule.ID, rule.ID+":fire", nil, 42, time.Now().UTC())
	if err != nil || !won {
		t.Fatalf("fire %v %v", won, err)
	}
	e.s = e.s.WithRollbackArtifactVerifier(stubRollbackArtifactVerifier{})
	read := func() api.AlertRollback {
		t.Helper()
		response := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/alert-rollbacks/"+fire, nil, nil)
		var r api.AlertRollback
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &r) != nil {
			t.Fatalf("receipt %d %s", response.Code, response.Body)
		}
		return r
	}
	if initial := read(); initial.DeploymentEvidence == nil || initial.DeploymentEvidence.CheckedAt != nil || initial.RollbackOperationID != "" {
		t.Fatalf("GET qualified a rollback %+v", initial)
	}
	if err = e.s.alertRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	blocked := read()
	if blocked.Code != "alert_rollback_evidence_insufficient" || blocked.RollbackOperationID != "" {
		t.Fatalf("missing evidence accepted %+v", blocked)
	}
	if err = e.store.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{AccountID: pgtype.UUID{Bytes: uuid.MustParse(e.acct.ID), Valid: true}, AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}, DeploymentID: pgtype.UUID{Bytes: uuid.MustParse(current.ID), Valid: true}, ReceivedAt: pgtype.Timestamptz{Time: blocked.DeploymentEvidence.WindowEnd.Add(-time.Minute), Valid: true}, Route: "__other__", Method: "GET", Status: 500, Count: 20, UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__"}); err != nil {
		t.Fatal(err)
	}
	old := e.s
	e.s = newServer(e.store, old.log, "gregale.dev", noopNotifier{}).WithRollbackArtifactVerifier(stubRollbackArtifactVerifier{})
	if err = e.s.alertRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	accepted := read()
	if accepted.RollbackOperationID != fire || accepted.DeploymentEvidence.Status != "breached" || accepted.DeploymentEvidence.ServerErrors != 20 {
		t.Fatalf("qualified intent %+v", accepted)
	}
	if err = e.store.MarkDeploymentLive(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	// A later rule change cannot invalidate already accepted cleanup.
	off := false
	if _, err = e.store.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if err = e.s.checkedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	if err = e.s.alertRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	complete := read()
	if complete.Status != "complete" || complete.AuditID == "" || !reflect.DeepEqual(accepted.DeploymentEvidence, complete.DeploymentEvidence) {
		t.Fatalf("restart cleanup %+v", complete)
	}
}
