//go:build metal && function_background_native

package e2e_test

// adr: 1576
// Run only with measured admission and actual native SQL witnesses.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest/functionsql"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFunctionMigrationAdmissionPostgresMetal(t *testing.T) {
	requireFunctionBackgroundNative(t)
	for _, runtime := range []string{"node22", "node24"} {
		for _, scenario := range []string{"cancel", "expiry", "capture-cancel", "probe-failure", "held-ACK-timeout"} {
			t.Run(runtime+"/"+scenario, func(t *testing.T) { runFunctionResourcesPostgresMetal(t, runtime, scenario) })
		}
	}
}

// This fixture uses the real standard materialization APIs before deployment;
// no native receipt or admission guard is removed to obtain a source capture.
func enrollNativeSQLMigration(t *testing.T, store *state.PgStore, appID, sqlIP string) {
	t.Helper()
	app, err := store.AppByID(t.Context(), appID)
	if err != nil || app.OrgID == "" {
		t.Fatal("migration SQL fixture requires an organization")
	}
	definition, err := json.Marshal(map[string]any{"egress_cidrs": map[string]any{"mode": "mandatory", "value": []string{sqlIP + "/32"}}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := store.PublishApplicationStandardVersion(t.Context(), state.ApplicationStandardPublish{OrgID: app.OrgID, ActorID: app.AccountID, Slug: "native-sql-migration", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: definition}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.PreviewApplicationStandardAssignment(t.Context(), app.OrgID, app.AccountID, state.ApplicationStandardReviewRequest{Scope: "application", ScopeID: app.ID, StandardID: v.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatal("native SQL standard review blocked", err)
	}
	op, err := store.ApproveApplicationStandardReview(t.Context(), p.OrgID, app.AccountID, p.ID, p.ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimApplicationStandardOperation(t.Context(), "native-sql-"+uuid.NewString())
	if err != nil || claim.OperationID != op.ID {
		t.Fatal("native SQL standard operation unavailable", err)
	}
	if _, err := store.MaterializeNextApplicationStandardTarget(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
}

func runNativeSQLMigration(t *testing.T, ctx context.Context, store *state.PgStore, vmm *sched.VMMClient,
	sql *functionsql.Fixture, proxy postgresfixture.Proxy, control *functionsql.Control, ins state.Instance,
	baseline map[int]nativeSQLReply, get func(string, int, string) nativeSQLReply, scenario string) {
	t.Helper()
	admission, err := store.GetInstanceApplicationStandardAdmission(ctx, ins.ID)
	if err != nil || !admission.Managed {
		t.Fatal("native migration cannot qualify an unmeasured source", err)
	}
	parent, err := store.GetInstanceApplicationStandardRuntimeReceipt(ctx, ins.ID)
	if err != nil || parent.Binding.ProtocolVersion != runtimeadmission.ArtifactProtocolVersion || runtimeadmission.CheckSnapshotParent(parent) != nil {
		t.Fatal("native migration requires retained measured consumption", err)
	}
	control.Arm()
	control.ArmRecovery()
	captureCtx, cancelCapture := context.WithCancel(ctx)
	defer cancelCapture()
	key := state.SnapshotCaptureMemKey(ins.DeploymentID, state.SnapshotTierWarm, uuid.NewString())
	type captureResult struct {
		prepared sched.LiveMigrationPrepare
		err      error
	}
	captured := make(chan captureResult, 1)
	go func() {
		prepared, err := vmm.PrepareLiveMigration(captureCtx, ins.NodeID, ins.ID, key)
		captured <- captureResult{prepared: prepared, err: err}
	}()
	backgroundNativeWait(t, ctx, func() bool {
		all, _ := sql.Connections(ctx, t)
		return control.Arrivals() == 4 && all == 0 && proxy.ConnectionCount() == 0
	})
	source, err := store.GetFunctionMigrationSourceByInstance(ctx, ins.ID)
	if err != nil || source.CaptureToken == "" || source.SourceWakeID != ins.WakeID || source.SourceNodeID != ins.NodeID {
		t.Fatal("checkpoint effects preceded durable measured source", err)
	}
	lease, err := store.GetMigrationLease(ctx, ins.ID, source.LeaseToken)
	if err != nil || lease.MemStorageKey != key || !lease.Pending {
		t.Fatal("capture lost pending durable handles", err)
	}
	select {
	case result := <-captured:
		t.Fatal("migration capture preceded checkpoint witness", result.err)
	default:
	}
	if scenario == "capture-cancel" {
		cancelCapture()
		select {
		case result := <-captured:
			if result.err == nil {
				t.Fatal("canceled capture returned success")
			}
		case <-ctx.Done():
			t.Fatal("canceled capture never returned")
		}
		control.ReleaseCheckpoint()
		backgroundNativeWait(t, ctx, func() bool {
			lease, err := store.GetMigrationLease(ctx, ins.ID, source.LeaseToken)
			return err == nil && !lease.Pending
		})
	} else {
		control.ReleaseCheckpoint()
		select {
		case result := <-captured:
			if result.err != nil || result.prepared.LeaseToken != source.LeaseToken || result.prepared.MemStorageKey != key {
				t.Fatal("measured source capture failed", result.err)
			}
		case <-ctx.Done():
			t.Fatal("measured source capture never returned")
		}
	}
	if scenario == "expiry" {
		if err := store.RenewMigrationLease(ctx, source.LeaseToken, ins.NodeID, time.Now().UTC().Add(-time.Microsecond)); err != nil {
			t.Fatal(err)
		}
	} else {
		if resolution, err := store.FenceFunctionMigrationSource(ctx, ins.ID, source.LeaseToken); err != nil || resolution != state.MigrationCommitAborted {
			t.Fatal("scheduler fixture could not fence rollback", err)
		}
	}
	if scenario == "probe-failure" {
		if _, err := sql.Admin.Exec(ctx, "ALTER TABLE notes RENAME TO notes_unavailable"); err != nil {
			t.Fatal("arm failed SQL readiness probe")
		}
		defer func() {
			if _, err := sql.Admin.Exec(context.WithoutCancel(ctx), "ALTER TABLE notes_unavailable RENAME TO notes"); err != nil {
				t.Error("restore SQL witness table")
			} else if len(sql.Rows(context.WithoutCancel(ctx), t)) != 3 {
				t.Error("failed probe lost or replayed committed SQL")
			}
		}()
	}
	type recoveryResult struct {
		retired bool
		err     error
	}
	recovered := make(chan recoveryResult, 1)
	if scenario != "expiry" {
		go func() {
			retired, err := vmm.CancelFunctionMigrationSource(ctx, ins.NodeID, ins.ID, source.LeaseToken)
			recovered <- recoveryResult{retired: retired, err: err}
		}()
	}
	fails := scenario == "capture-cancel" || scenario == "probe-failure" || scenario == "held-ACK-timeout"
	if scenario != "capture-cancel" && scenario != "probe-failure" {
		backgroundNativeWait(t, ctx, func() bool {
			all, _ := sql.Connections(ctx, t)
			return control.RecoveryArrivals() == 4 && all == 8
		})
		current, err := store.MigrationInstanceByID(ctx, ins.ID)
		if err != nil || current.State != string(state.StateMigrating) || current.WakeID != ins.WakeID || current.NodeID != ins.NodeID || current.LeaseToken != state.FunctionMigrationRollbackToken(source.LeaseToken) {
			t.Fatal("source published before final recovery ACK", err)
		}
		if _, err := vmm.FreezeInstance(ctx, ins.ID, false); err == nil {
			t.Fatal("ordinary lifecycle bypassed held recovery ACK")
		}
		if scenario != "held-ACK-timeout" {
			control.Release()
		}
	}
	if scenario == "expiry" {
		// The real schedd watchdog must fence, recover, complete and clean up.
		backgroundNativeWait(t, ctx, func() bool {
			current, err := store.MigrationInstanceByID(ctx, ins.ID)
			_, sourceErr := store.GetFunctionMigrationSource(ctx, ins.ID, source.LeaseToken)
			return err == nil && current.State == string(state.StateRunning) && current.WakeID == ins.WakeID && errors.Is(sourceErr, state.ErrNotFound)
		})
	} else {
		select {
		case result := <-recovered:
			if result.err != nil || result.retired != fails {
				t.Fatal("wrong physical source recovery result", result.retired, result.err)
			}
		case <-ctx.Done():
			t.Fatal("source recovery did not settle")
		}
		control.Release()
		if !fails {
			if retired, err := vmm.CancelFunctionMigrationSource(ctx, ins.NodeID, ins.ID, source.LeaseToken); err != nil || retired {
				t.Fatal("duplicate cancel lost confirmed source result", err)
			}
		}
		if err := store.CompleteFunctionMigrationSource(ctx, ins.ID, source.LeaseToken, fails); err != nil {
			t.Fatal("scheduler fixture could not complete physical result", err)
		}
		if _, err := vmm.CancelFunctionMigrationSource(ctx, ins.NodeID, ins.ID, source.LeaseToken); err != nil {
			t.Fatal("completed source cleanup failed", err)
		}
	}
	if scenario != "probe-failure" && len(sql.Rows(ctx, t)) != 3 {
		t.Fatal("source lifecycle lost or replayed committed SQL")
	}
	if !fails {
		continued := get("state", 200, "")
		requireNativeSQLGeneration(t, baseline[continued.PID], continued, 1)
	}
}
