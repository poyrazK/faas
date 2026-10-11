package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func seedHistoricalDeploymentClock(ctx context.Context, t *testing.T, s state.Store, pool *pgxpool.Pool, d state.Deployment) state.Deployment {
	t.Helper()
	cutover := time.Now().UTC().Truncate(time.Minute).Add(-5*time.Minute + 10*time.Second)
	if _, err := pool.Exec(ctx, "UPDATE deployments SET rollout_completed_at=$2 WHERE id=$1", d.ID, cutover); err != nil {
		t.Fatal(err)
	}
	d, err := s.DeploymentByID(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func seedHistoricalRequest(ctx context.Context, t *testing.T, s state.Store, account, app, deployment string, at time.Time, status, count int32) {
	t.Helper()
	if err := s.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{AccountID: pgtype.UUID{Bytes: uuid.MustParse(account), Valid: true},
		AppID: pgtype.UUID{Bytes: uuid.MustParse(app), Valid: true}, DeploymentID: pgtype.UUID{Bytes: uuid.MustParse(deployment), Valid: true},
		ReceivedAt: pgtype.Timestamptz{Time: at, Valid: true}, Route: "__other__", Method: "GET", Status: status, Count: count,
		UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__"}); err != nil {
		t.Fatal(err)
	}
}

func TestHistoricalAlertDeploymentEvidencePG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	for _, scenario := range []string{"healthy replacement", "actual breach", "delayed ingestion", "low traffic", "stale samples", "threshold changed", "window changed", "cutover changed", "unsupported metric"} {
		t.Run(scenario, func(t *testing.T) {
			acct, app, predecessor, current := checkedRollbackFixtureContext(ctx, t, s, false)
			current = seedHistoricalDeploymentClock(ctx, t, s, pool, current)
			rule := alertRollbackRule(ctx, t, s, acct, app)
			window := 600
			var err error
			rule, err = s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{PostDeployRollbackWindowSeconds: &window})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "unsupported metric" {
				metric := state.AlertMetricRequestCount
				rule, err = s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{Metric: &metric})
				if err != nil {
					t.Fatal(err)
				}
			}
			fire := claimAlertRollback(ctx, t, s, rule)
			if fire.DeploymentEvidence == nil {
				t.Fatal("fire lost rule snapshot")
			}
			e := fire.DeploymentEvidence
			at := e.WindowEnd.Add(-time.Minute)
			// Old release errors, another account/app/deployment, the cutover
			// minute and unclosed minutes must not enter the candidate aggregate.
			for _, labels := range [][3]string{{acct.ID, app.ID, predecessor.ID}, {uuid.NewString(), app.ID, current.ID}, {acct.ID, uuid.NewString(), current.ID}, {acct.ID, app.ID, uuid.NewString()}} {
				seedHistoricalRequest(ctx, t, s, labels[0], labels[1], labels[2], at, 500, 1000)
			}
			seedHistoricalRequest(ctx, t, s, acct.ID, app.ID, current.ID, e.CutoverAt.Truncate(time.Minute), 500, 1000)
			seedHistoricalRequest(ctx, t, s, acct.ID, app.ID, current.ID, e.WindowEnd, 500, 1000)
			// The app alert ignores 4xx responses in its denominator.
			seedHistoricalRequest(ctx, t, s, acct.ID, app.ID, current.ID, at, 400, 1000)
			status, count := int32(500), int32(20)
			if scenario == "healthy replacement" {
				status = 200
			}
			if scenario == "low traffic" {
				count = 19
			}
			if scenario == "stale samples" {
				at = e.WindowEnd.Add(-3 * time.Minute)
			}
			if scenario != "delayed ingestion" {
				seedHistoricalRequest(ctx, t, s, acct.ID, app.ID, current.ID, at, status, count)
			}
			switch scenario {
			case "threshold changed":
				value := float64(99)
				_, err = s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{Threshold: &value})
			case "window changed":
				value := state.AlertWindow15m
				_, err = s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{WindowSpec: &value})
			case "cutover changed":
				_, err = pool.Exec(ctx, "UPDATE deployments SET rollout_completed_at=rollout_completed_at+interval '1 second' WHERE id=$1", current.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			var ledger state.AlertRollbackStore = s
			got, err := ledger.CommitAlertRollback(ctx, fire)
			if scenario == "threshold changed" || scenario == "window changed" || scenario == "cutover changed" {
				if !errors.Is(err, state.ErrAlertRollbackChanged) {
					t.Fatalf("changed snapshot accepted %+v %v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"healthy replacement": "alert_rollback_deployment_healthy", "low traffic": "alert_rollback_evidence_insufficient", "stale samples": "alert_rollback_evidence_stale", "delayed ingestion": "alert_rollback_evidence_insufficient", "unsupported metric": "alert_rollback_metric_unsupported"}[scenario]
			if want != "" {
				if got.Code != want || got.RollbackOperationID != "" {
					t.Fatalf("unqualified intent %+v", got)
				}
				if scenario == "low traffic" && got.DeploymentEvidence.ErrorRatePct != 100 {
					t.Fatalf("insufficient traffic lost the observed error rate: %+v", got.DeploymentEvidence)
				}
				if scenario != "delayed ingestion" {
					return
				}
				seedHistoricalRequest(ctx, t, s, acct.ID, app.ID, current.ID, at, 500, 20)
				// A replacement store reads the same pinned fire after ingestion.
				ledger = state.NewPgStore(pool)
				got, err = ledger.CommitAlertRollback(ctx, fire)
			}
			if err != nil || got.RollbackOperationID != fire.ID || got.DeploymentEvidence.Status != "breached" || got.DeploymentEvidence.Requests != 20 || got.DeploymentEvidence.ServerErrors != 20 {
				t.Fatalf("breach not accepted %+v %v", got, err)
			}
			seedHistoricalRequest(ctx, t, s, acct.ID, app.ID, current.ID, at, 200, 10000)
			replay, err := ledger.CommitAlertRollback(ctx, fire)
			if err != nil || !reflect.DeepEqual(replay.DeploymentEvidence, got.DeploymentEvidence) {
				t.Fatalf("accepted evidence changed %+v %v", replay, err)
			}
			audits, err := s.ListDeploymentAuditByAlertRule(ctx, rule.ID, 100)
			var payload struct {
				Evidence *api.AlertRollbackDeploymentEvidence `json:"deployment_evidence"`
			}
			if err != nil || len(audits) != 1 || json.Unmarshal(audits[0].Data, &payload) != nil || !reflect.DeepEqual(payload.Evidence, got.DeploymentEvidence) {
				t.Fatalf("intent audit lost evidence %+v %v", audits, err)
			}
		})
	}
}

func TestHistoricalAlertEvidenceExpiresWhileWaitingForTargetPG(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	acct, app, target, current := checkedRollbackFixtureContext(ctx, t, s, false)
	current = seedHistoricalDeploymentClock(ctx, t, s, pool, current)
	rule := alertRollbackRule(ctx, t, s, acct, app)
	window := 600
	rule, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{PostDeployRollbackWindowSeconds: &window})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-api.AlertRollbackEvidenceMaxCheckDelay + 3*time.Second)
	id, won, err := s.ClaimAlertFire(ctx, rule.ID, rule.ID+":deadline", nil, 42, at)
	if err != nil || !won {
		t.Fatalf("fire %v %v", won, err)
	}
	fire, err := s.ReadAlertRollback(ctx, id)
	if err != nil || fire.DeploymentEvidence == nil {
		t.Fatalf("snapshot %+v %v", fire, err)
	}
	seedHistoricalRequest(ctx, t, s, acct.ID, app.ID, current.ID, fire.DeploymentEvidence.WindowEnd.Add(-time.Minute), 500, 20)
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err = holder.Exec(ctx, "SELECT id FROM deployments WHERE id=$1 FOR UPDATE", target.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := s.CommitAlertRollback(ctx, fire); result <- err }()
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%CheckedRollbackTargetFacts%')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("intent did not wait for target: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	remaining := time.Until(fire.FiredAt.Add(api.AlertRollbackEvidenceMaxCheckDelay)) + 20*time.Millisecond
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(remaining):
	}
	if err = holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, state.ErrAlertRollbackEvidenceExpired) {
		t.Fatalf("expired evidence accepted: %v", err)
	}
	if _, err = s.GetCheckedRollback(ctx, acct.ID, app.ID, fire.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rolled-back intent remains: %v", err)
	}
	retained, err := s.DeploymentByID(ctx, target.ID)
	if err != nil || retained.Status != state.DeploySuperseded {
		t.Fatalf("failed transaction prepared target %+v %v", retained, err)
	}
	audits, err := s.ListDeploymentAuditByAlertRule(ctx, rule.ID, 100)
	if err != nil || len(audits) != 0 {
		t.Fatalf("expired intent audited %+v %v", audits, err)
	}
}
