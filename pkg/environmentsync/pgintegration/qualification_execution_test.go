// adr: 493 — qualification capacity follows physical attempt retirement.
package pgintegration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// These receipts are storage fixtures, not native process evidence.
func qualificationNativeProof() state.EnvironmentQualificationRetirement {
	return state.EnvironmentQualificationRetirement{Kind: state.QualificationNativeRetired, ReceiptID: uuid.NewString(),
		NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(), ProcessesExited: true, ResourcesRemoved: true}
}

func retireQualificationWithoutDispatch(t *testing.T, store state.Store, instanceID string) {
	t.Helper()
	executor := store.(state.EnvironmentQualificationExecutionStore)
	status, err := executor.EnvironmentQualificationExecution(t.Context(), instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.RetireEnvironmentQualificationExecution(t.Context(), status.Execution,
		state.EnvironmentQualificationRetirement{Kind: state.QualificationNeverDispatched}); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentQualificationExecutionBindsDispatchAndRetirement(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		_, _, requests := preparedQualificationFixture(t, store)
		qualifier, executor := store.(state.EnvironmentGitOpsQualificationStore), store.(state.EnvironmentQualificationExecutionStore)
		claimed, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "scheduler", time.Second)
		if err != nil {
			t.Fatal(err)
		}
		placement := qualificationPlacement(t, store, 4096)
		admission, err := store.(state.EnvironmentGitOpsQualificationInstanceStore).CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, placement)
		if err != nil {
			t.Fatal(err)
		}
		frame := admission.Execution
		if frame.InstanceID != claimed.ReservedInstanceID || frame.RequestID != claimed.ID || frame.NodeID != placement.NodeID || frame.WakeID != placement.WakeID || frame.Attempt != claimed.Attempt || frame.Artifact != claimed.Artifact || frame.CleanupToken == "" {
			t.Fatal("lost attempt placement", frame)
		}
		wire, _ := json.Marshal(frame)
		if strings.Contains(string(wire), frame.CleanupToken) || strings.Contains(string(wire), claimed.LeaseToken) {
			t.Fatal("cleanup capability entered public JSON")
		}
		if strings.Contains(fmt.Sprintf("%v %#v", frame, frame), frame.CleanupToken) {
			t.Fatal("cleanup capability entered diagnostics")
		}
		if err := executor.MarkEnvironmentQualificationDispatched(t.Context(), claimed, frame); err != nil {
			t.Fatal(err)
		}
		if err := executor.MarkEnvironmentQualificationDispatched(t.Context(), claimed, frame); !errors.Is(err, state.ErrConflict) {
			t.Fatal("dispatch replay allowed", err)
		}
		if err := executor.RetireEnvironmentQualificationExecution(t.Context(), frame, state.EnvironmentQualificationRetirement{Kind: state.QualificationNeverDispatched}); !errors.Is(err, state.ErrConflict) {
			t.Fatal("dispatched VM accepted no-dispatch proof", err)
		}
		proof := qualificationNativeProof()
		for _, field := range []string{"instance", "node", "wake", "attempt", "token", "artifact", "source", "generation", "exited", "resources", "boot", "receipt_alias"} {
			forged, missing := frame, proof
			switch field {
			case "instance":
				forged.InstanceID = uuid.NewString()
			case "node":
				forged.NodeID = uuid.NewString()
			case "wake":
				forged.WakeID = uuid.NewString()
			case "attempt":
				forged.Attempt++
			case "token":
				forged.CleanupToken = uuid.NewString()
			case "artifact":
				forged.Artifact.RootfsKey = "substitute"
			case "source":
				forged.SourceID = uuid.NewString()
			case "generation":
				forged.Generation++
			case "exited":
				missing.ProcessesExited = false
			case "resources":
				missing.ResourcesRemoved = false
			case "boot":
				missing.KernelBootID = ""
			case "receipt_alias":
				missing.ReceiptID = "urn:uuid:" + proof.ReceiptID
			}
			if err := executor.RetireEnvironmentQualificationExecution(t.Context(), forged, missing); err == nil {
				t.Fatal("accepted forged retirement", field)
			}
		}
		for _, terminal := range []state.State{state.StateStopped, state.StateFailed, state.StateParked, state.StateEvictingAccountDeleting} {
			if err := store.UpdateInstanceState(t.Context(), frame.InstanceID, string(terminal)); err == nil {
				t.Fatal("generic state released native reservation", terminal)
			}
		}
		time.Sleep(max(0, time.Until(*claimed.LeaseUntil)+20*time.Millisecond))
		if _, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), claimed.ID, "replacement", time.Minute); !errors.Is(err, state.ErrConflict) {
			t.Fatal("expiry released unfinished attempt", err)
		}
		if err := executor.RetireEnvironmentQualificationExecution(t.Context(), frame, proof); err != nil {
			t.Fatal("expired attempt could not retire", err)
		}
		if err := executor.RetireEnvironmentQualificationExecution(t.Context(), frame, proof); err != nil {
			t.Fatal("lost retirement response not idempotent", err)
		}
		changed := proof
		changed.ReceiptID = uuid.NewString()
		if err := executor.RetireEnvironmentQualificationExecution(t.Context(), frame, changed); !errors.Is(err, state.ErrConflict) {
			t.Fatal("retirement receipt replaced", err)
		}
		sibling, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), requests[1].ID, "worker-scheduler", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		placement.WakeID = uuid.NewString()
		other, err := store.(state.EnvironmentGitOpsQualificationInstanceStore).CreateEnvironmentWorkloadQualificationInstance(t.Context(), sibling, placement)
		if err != nil {
			t.Fatal(err)
		}
		if err := executor.MarkEnvironmentQualificationDispatched(t.Context(), sibling, other.Execution); err != nil {
			t.Fatal(err)
		}
		if err := executor.RetireEnvironmentQualificationExecution(t.Context(), other.Execution, proof); err == nil {
			t.Fatal("one native receipt retired two attempts")
		}
		changed.ReceiptID = uuid.NewString()
		if err := executor.RetireEnvironmentQualificationExecution(t.Context(), other.Execution, changed); err == nil {
			t.Fatal("one native incarnation retired two attempts")
		}
		status, err := executor.EnvironmentQualificationExecution(t.Context(), frame.InstanceID)
		ins, insErr := store.InstanceByID(t.Context(), frame.InstanceID)
		terminal, terminalErr := store.ListInstancesInTerminalStatesOlderThan(t.Context(), []state.State{state.StateStopped}, time.Now().Add(time.Second))
		// Legacy InstanceByID intentionally omits terminal_at in PostgreSQL;
		// the retention projection reads the actual committed terminal stamp.
		var stamp *time.Time
		for _, row := range terminal {
			if row.ID == frame.InstanceID {
				stamp = row.TerminalAt
			}
		}
		if err != nil || insErr != nil || terminalErr != nil || status.RetiredAt == nil || stamp == nil || !status.RetiredAt.Equal(*stamp) || ins.State != string(state.StateStopped) {
			t.Fatal("receipt and terminal state did not commit together", status, ins, err, insErr)
		}
		replacement, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), claimed.ID, "replacement", time.Minute)
		if err != nil || replacement.Attempt != frame.Attempt+1 || replacement.ReservedInstanceID == frame.InstanceID {
			t.Fatal("retirement failed to issue fresh identity", replacement, err)
		}
		if err := store.DeleteInstance(t.Context(), frame.InstanceID); err != nil {
			t.Fatal(err)
		}
		if _, err := executor.EnvironmentQualificationExecution(t.Context(), frame.InstanceID); err != nil {
			t.Fatal("instance collection erased original frame", err)
		}
	})
}

func TestEnvironmentQualificationExecutionKeepsCleanupAfterParentPurge(t *testing.T) {
	stores(t, func(t *testing.T, store gitOpsTestStore) {
		lease, _, requests := preparedQualificationFixture(t, store)
		claimed, err := store.(state.EnvironmentGitOpsQualificationStore).ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "scheduler", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		admission, err := store.(state.EnvironmentGitOpsQualificationInstanceStore).CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, qualificationPlacement(t, store, 4096))
		if err != nil {
			t.Fatal(err)
		}
		executor := store.(state.EnvironmentQualificationExecutionStore)
		if err := executor.MarkEnvironmentQualificationDispatched(t.Context(), claimed, admission.Execution); err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteProject(t.Context(), lease.Source.ProjectID); err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteInstance(t.Context(), claimed.ReservedInstanceID); err == nil {
			t.Fatal("source deletion erased native obligation")
		}
		if err := store.NodeSetLifecycle(t.Context(), admission.Execution.NodeID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
			t.Fatal(err)
		}
		if err := executor.RetireEnvironmentQualificationExecution(t.Context(), admission.Execution, qualificationNativeProof()); err != nil {
			t.Fatal("purged execution could not retire on original node", err)
		}
	})
}

func TestPgEnvironmentQualificationExecutionRejectsRawIdentityAndTerminalWrites(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	_, _, requests := preparedQualificationFixture(t, store)
	claimed, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "scheduler", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := store.CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, qualificationPlacement(t, store, 4096))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`update environment_qualification_executions set frame=jsonb_set(frame,'{node_id}',to_jsonb(gen_random_uuid()::text)) where instance_id=$1`,
		`update environment_qualification_executions set cleanup_token=gen_random_uuid() where instance_id=$1`,
		`update environment_qualification_executions set dispatch_started=true where instance_id=$1`,
		`delete from environment_qualification_executions where instance_id=$1`,
		`update instances set state='stopped',terminal_at=clock_timestamp() where id=$1`,
		`delete from instances where id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), statement, admission.Instance.ID); err == nil {
			t.Fatal("raw write bypassed retirement fence", statement)
		}
	}
	if err := store.MarkEnvironmentQualificationDispatched(t.Context(), claimed, admission.Execution); err != nil {
		t.Fatal(err)
	}
	alias, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alias.Exec(t.Context(), `select set_config('gregale.gitops_qualification_cleanup',$1,true)`, admission.Execution.CleanupToken); err != nil {
		_ = alias.Rollback(t.Context())
		t.Fatal(err)
	}
	noncanonical := qualificationNativeProof()
	noncanonical.ReceiptID = "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"
	data, _ := json.Marshal(noncanonical)
	_, err = alias.Exec(t.Context(), `update environment_qualification_executions set retirement=$2,retired_at=clock_timestamp() where instance_id=$1`, admission.Instance.ID, data)
	_ = alias.Rollback(t.Context())
	if err == nil || !strings.Contains(err.Error(), "native_receipt_canonical") {
		t.Fatal("native identity spelling bypassed uniqueness", err)
	}
	if err := store.RetireEnvironmentQualificationExecution(t.Context(), admission.Execution, qualificationNativeProof()); err != nil {
		t.Fatal(err)
	}
	// Native evidence and terminal state are one transaction; migration replay
	// must preserve their exact original frame rather than manufacture authority.
	before, err := store.EnvironmentQualificationExecution(t.Context(), admission.Instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	after, err := store.EnvironmentQualificationExecution(t.Context(), admission.Instance.ID)
	if err != nil || before.Execution != after.Execution || after.RetiredAt == nil || !before.RetiredAt.Equal(*after.RetiredAt) {
		t.Fatal("migration replay changed cleanup identity", err)
	}
}
