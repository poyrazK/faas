package release

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestDecideRollbackOutcome(t *testing.T) {
	requested := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	served := requested.Add(8300 * time.Millisecond)
	earlier := requested.Add(-time.Hour)
	record := state.RollbackRecord{ID: "r", RequestedAt: requested}
	tests := []struct {
		name       string
		target     state.Deployment
		targetErr  error
		now        time.Time
		wantDone   bool
		wantStatus string
		wantCode   string
		wantMS     int64
	}{
		{name: "still activating", target: state.Deployment{Status: state.DeploySnapshotting}, now: requested.Add(time.Minute)},
		{name: "served", target: state.Deployment{Status: state.DeployLive, TrafficPercent: 100, RolloutCompletedAt: &served},
			now: requested.Add(time.Minute), wantDone: true, wantStatus: state.RollbackStatusComplete, wantMS: 8300},
		{name: "served then replaced before the check", target: state.Deployment{Status: state.DeploySuperseded, RolloutCompletedAt: &served},
			now: requested.Add(time.Minute), wantDone: true, wantStatus: state.RollbackStatusComplete, wantMS: 8300},
		{name: "superseded without serving", target: state.Deployment{Status: state.DeploySuperseded},
			now: requested.Add(time.Minute), wantDone: true, wantStatus: state.RollbackStatusFailed, wantCode: "rollback_target_not_activated"},
		{name: "stale completion from an earlier release does not count", target: state.Deployment{Status: state.DeploySuperseded, RolloutCompletedAt: &earlier},
			now: requested.Add(time.Minute), wantDone: true, wantStatus: state.RollbackStatusFailed, wantCode: "rollback_target_not_activated"},
		{name: "smoke failure keeps its code", target: state.Deployment{Status: state.DeploySuperseded, ErrorCode: api.CodeDeploymentSmokeFailed, Error: "smoke failed"},
			now: requested.Add(time.Minute), wantDone: true, wantStatus: state.RollbackStatusFailed, wantCode: api.CodeDeploymentSmokeFailed},
		{name: "target deleted", targetErr: state.ErrNotFound,
			now: requested.Add(time.Minute), wantDone: true, wantStatus: state.RollbackStatusFailed, wantCode: "rollback_target_missing"},
		{name: "never served before the deadline", target: state.Deployment{Status: state.DeploySnapshotting},
			now: requested.Add(api.RollbackOutcomeTimeout), wantDone: true, wantStatus: state.RollbackStatusFailed, wantCode: "rollback_timeout"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decideRollbackOutcome(record, tc.target, tc.targetErr, tc.now)
			if got.done != tc.wantDone || got.status != tc.wantStatus || got.errorCode != tc.wantCode {
				t.Fatalf("verdict = %+v; want done=%v status=%q code=%q", got, tc.wantDone, tc.wantStatus, tc.wantCode)
			}
			if tc.wantStatus == state.RollbackStatusComplete && got.recovery.Milliseconds() != tc.wantMS {
				t.Fatalf("recovery = %s, want %dms", got.recovery, tc.wantMS)
			}
		})
	}
}

type recordingAuditor struct{ kinds []string }

func (a *recordingAuditor) Emit(_ context.Context, kind string, _ *string, _ map[string]any) {
	a.kinds = append(a.kinds, kind)
}

// A plain rollback opens a preparing receipt, and the outcome worker
// completes it only once the target actually serves, with the measured
// recovery time, a committed audit id, and the outcome metric.
func TestRollbacksVerifyTheTargetServed(t *testing.T) {
	for _, served := range []bool{true, false} {
		t.Run(map[bool]string{true: "served", false: "never served"}[served], func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "rollback-outcome@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "rollback-outcome", RAMMB: 256, MaxConcurrency: 5, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			deploy := func(digest string) state.Deployment {
				d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, ImageDigest: digest, Status: state.DeployPending})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
					t.Fatal(err)
				}
				return d
			}
			target, current := deploy("sha256:old"), deploy("sha256:new")
			if _, err := store.PrepareDeploymentRollback(ctx, app.ID, target.ID); err != nil {
				t.Fatalf("PrepareDeploymentRollback: %v", err)
			}
			audit, ops := &recordingAuditor{}, wire.NewOpsMetrics("apid")
			rollbacks := Rollbacks{Store: store, Log: slog.Default(), Audit: audit, Metrics: ops}
			operation := rollbacks.Record(ctx, acct, app, target, current.ID, state.RollbackTriggerAuto5xx)
			if operation == nil || operation.Status != state.RollbackStatusPreparing || operation.Trigger != state.RollbackTriggerAuto5xx {
				t.Fatalf("receipt = %+v; want a preparing auto_5xx receipt", operation)
			}
			receipt := func() api.RollbackOperation {
				t.Helper()
				got, ok := rollbacks.Operation(ctx, acct.ID, app.ID, operation.ID)
				if !ok {
					t.Fatalf("rollback receipt %s not found", operation.ID)
				}
				return got
			}

			// Nothing has served yet: the sweep must not resolve the rollback.
			if err := rollbacks.Sweep(ctx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if got := receipt(); got.Status != state.RollbackStatusPreparing {
				t.Fatalf("status before activation = %q, want preparing", got.Status)
			}

			if served {
				if err := store.MarkDeploymentLive(ctx, target.ID); err != nil {
					t.Fatalf("promote rollback target: %v", err)
				}
			} else if err := store.UpdateDeploymentStatus(ctx, target.ID, state.DeploySuperseded, ""); err != nil {
				t.Fatal(err)
			}
			if err := rollbacks.Sweep(ctx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			got := receipt()
			if served {
				if got.Status != state.RollbackStatusComplete || got.RecoveryMS == nil || got.AuditID == "" || got.CompletedAt == nil {
					t.Fatalf("receipt = %+v; want complete with recovery, audit id, and completion time", got)
				}
				if len(audit.kinds) != 1 || audit.kinds[0] != "app.rollback_completed" {
					t.Fatalf("audit events = %v, want [app.rollback_completed]", audit.kinds)
				}
			} else {
				if got.Status != state.RollbackStatusFailed || got.Code != "rollback_target_not_activated" || !strings.Contains(got.Error, "without serving") {
					t.Fatalf("receipt = %+v; want failed with rollback_target_not_activated", got)
				}
				if len(audit.kinds) != 1 || audit.kinds[0] != "app.rollback_failed" {
					t.Fatalf("audit events = %v, want [app.rollback_failed]", audit.kinds)
				}
			}
			if n := testutil.ToFloat64(ops.RollbackOutcomesCounter(state.RollbackTriggerAuto5xx, got.Status)); n != 1 {
				t.Fatalf("%s outcome counter = %g, want 1", got.Status, n)
			}
			// A second sweep must not resolve or count the rollback again.
			if err := rollbacks.Sweep(ctx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if n := testutil.ToFloat64(ops.RollbackOutcomesCounter(state.RollbackTriggerAuto5xx, got.Status)); n != 1 || len(audit.kinds) != 1 {
				t.Fatalf("outcome counted %g times with %d audit events, want once", n, len(audit.kinds))
			}
		})
	}
}

// A ledger-less store or nil seams must not break a rollback request.
func TestRollbacksWithoutLedgerOrSeams(t *testing.T) {
	ctx := context.Background()
	rollbacks := Rollbacks{Store: state.Store(nil), Log: slog.Default()}
	if op := rollbacks.Record(ctx, state.Account{}, state.App{}, state.Deployment{}, "", state.RollbackTriggerManual); op != nil {
		t.Fatalf("Record without a ledger = %+v, want nil", op)
	}
	if _, ok := rollbacks.Operation(ctx, "a", "b", "c"); ok {
		t.Fatal("Operation without a ledger reported a receipt")
	}
	if err := rollbacks.Sweep(ctx, time.Now()); err != nil {
		t.Fatalf("Sweep without a ledger = %v", err)
	}
}

func TestRecordRollbackRequestSupersedesAPreparingReceipt(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	first, err := store.RecordRollbackRequest(ctx, state.RollbackRecord{AccountID: "a", AppID: "b", TargetDeploymentID: "t", Trigger: state.RollbackTriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordRollbackRequest(ctx, state.RollbackRecord{AccountID: "a", AppID: "b", TargetDeploymentID: "t", Trigger: state.RollbackTriggerManual}); err != nil {
		t.Fatal(err)
	}
	got, err := store.RollbackRecordByID(ctx, "a", "b", first.ID)
	if err != nil || got.Status != state.RollbackStatusFailed || got.ErrorCode != "rollback_superseded" {
		t.Fatalf("first receipt = %+v, %v; want failed rollback_superseded", got, err)
	}
	if _, err := store.RollbackRecordByID(ctx, "other-account", "b", first.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account read = %v, want ErrNotFound", err)
	}
}
