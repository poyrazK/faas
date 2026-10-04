// adr: 532 — recovery discovery cannot revoke a renewed execution lease.
package pgintegration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestEnvironmentQualificationRecoveryDiscoveryRetainsOriginalHostAfterPurge(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, _, requests := preparedQualificationFixture(t, basic)
		store := basic.(state.Store)
		discovery := basic.(state.EnvironmentQualificationRecoveryStore)
		placement := qualificationPlacement(t, store, 4096)
		frames := map[string]state.EnvironmentQualificationExecution{}
		for _, request := range requests {
			claimed, err := basic.(state.EnvironmentGitOpsQualificationStore).ClaimEnvironmentWorkloadQualification(t.Context(), request.ID, "scheduler", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			placement.WakeID = uuid.NewString()
			admission, err := basic.(state.EnvironmentGitOpsQualificationInstanceStore).CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, placement)
			if err != nil {
				t.Fatal(err)
			}
			frames[admission.Instance.ID] = admission.Execution
			if _, err := discovery.EnvironmentQualificationExecutionForRecovery(t.Context(), placement.NodeID, admission.Instance.ID); !errors.Is(err, state.ErrConflict) {
				t.Fatal("recovery stole a valid lease", err)
			}
			if _, err := discovery.EnvironmentQualificationExecutionForRecovery(t.Context(), uuid.NewString(), admission.Instance.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("recovery substituted another host", err)
			}
		}
		if rows, err := discovery.ListEnvironmentQualificationExecutionsForRecovery(t.Context(), placement.NodeID, "", 1); err != nil || len(rows) != 0 {
			t.Fatal("active qualification entered recovery discovery", len(rows), err)
		}
		for _, args := range []struct {
			node, cursor string
			limit        int
		}{
			{"", "", 1}, {uuid.Nil.String(), "", 1}, {placement.NodeID, "not-a-cursor", 1},
			{placement.NodeID, "", 0}, {placement.NodeID, "", api.EnvironmentGitOpsQualificationRecoveryBatchMax + 1},
		} {
			if _, err := discovery.ListEnvironmentQualificationExecutionsForRecovery(t.Context(), args.node, args.cursor, args.limit); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatal("unbounded or invalid recovery page accepted", err)
			}
		}
		if err := store.DeleteProject(t.Context(), lease.Source.ProjectID); err != nil {
			t.Fatal(err)
		}
		if err := store.NodeSetLifecycle(t.Context(), placement.NodeID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
			t.Fatal(err)
		}
		cursor := ""
		for range len(frames) {
			rows, err := discovery.ListEnvironmentQualificationExecutionsForRecovery(t.Context(), placement.NodeID, cursor, 1)
			if err != nil || len(rows) != 1 || rows[0].Execution.InstanceID <= cursor {
				t.Fatal("recovery cursor lost or repeated a holding", len(rows), err)
			}
			status := rows[0]
			if status.Execution != frames[status.Execution.InstanceID] || status.DispatchStarted || status.RetiredAt != nil {
				t.Fatal("purge changed original execution frame")
			}
			checked, err := discovery.EnvironmentQualificationExecutionForRecovery(t.Context(), placement.NodeID, status.Execution.InstanceID)
			if err != nil || checked.Execution != status.Execution {
				t.Fatal("original host cleanup unavailable after purge", err)
			}
			retireQualificationWithoutDispatch(t, store, status.Execution.InstanceID)
			cursor = status.Execution.InstanceID
		}
		if rows, err := discovery.ListEnvironmentQualificationExecutionsForRecovery(t.Context(), placement.NodeID, "", 1); err != nil || len(rows) != 0 {
			t.Fatal("retired frames reentered pending discovery", len(rows), err)
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := discovery.ListEnvironmentQualificationExecutionsForRecovery(cancelled, placement.NodeID, "", 1); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled discovery continued", err)
		}
	})
}

func TestEnvironmentQualificationRecoveryDiscoversExpiredOriginalAttempt(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		_, _, requests := preparedQualificationFixture(t, basic)
		qualifier := basic.(state.EnvironmentGitOpsQualificationStore)
		claimed, err := qualifier.ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "scheduler", time.Second)
		if err != nil {
			t.Fatal(err)
		}
		placement := qualificationPlacement(t, basic, 4096)
		admission, err := basic.(state.EnvironmentGitOpsQualificationInstanceStore).CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, placement)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(max(0, time.Until(*claimed.LeaseUntil)+20*time.Millisecond))
		discovery := basic.(state.EnvironmentQualificationRecoveryStore)
		rows, err := discovery.ListEnvironmentQualificationExecutionsForRecovery(t.Context(), placement.NodeID, "", 1)
		if err != nil || len(rows) != 1 || rows[0].Execution != admission.Execution {
			t.Fatal("expired attempt lost its original holding", len(rows), err)
		}
		if _, err := qualifier.RenewEnvironmentWorkloadQualification(t.Context(), claimed, time.Minute); !errors.Is(err, state.ErrConflict) {
			t.Fatal("expired lease regained dispatch authority", err)
		}
		checked, err := discovery.EnvironmentQualificationExecutionForRecovery(t.Context(), placement.NodeID, admission.Instance.ID)
		if err != nil || checked.Execution != admission.Execution {
			t.Fatal("expired frame failed locked eligibility check", err)
		}
	})
}

func TestPgEnvironmentQualificationRecoveryRechecksUncommittedRenewal(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	_, _, requests := preparedQualificationFixture(t, store)
	claimed, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), requests[0].ID, "scheduler", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	placement := qualificationPlacement(t, store, 4096)
	admission, err := store.CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, placement)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	q := sqlc.New()
	if _, err := q.SetEnvironmentWorkloadQualificationContext(t.Context(), tx, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	row, err := q.RenewEnvironmentWorkloadQualification(t.Context(), tx, sqlc.RenewEnvironmentWorkloadQualificationParams{
		ID: pgtype.UUID{Bytes: uuid.MustParse(claimed.ID), Valid: true}, Token: claimed.LeaseToken, Attempt: claimed.Attempt, DurationUs: time.Minute.Microseconds()})
	if err != nil || !row.LeaseUntil.Time.After(*claimed.LeaseUntil) {
		t.Fatal("prepare uncommitted renewal", err)
	}
	time.Sleep(max(0, time.Until(*claimed.LeaseUntil)+20*time.Millisecond))
	rows, err := store.ListEnvironmentQualificationExecutionsForRecovery(t.Context(), placement.NodeID, "", 1)
	if err != nil || len(rows) != 1 {
		t.Fatal("fixture did not expose stale discovery snapshot", len(rows), err)
	}
	checked := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	go func() {
		_, err := store.EnvironmentQualificationExecutionForRecovery(ctx, placement.NodeID, admission.Instance.ID)
		checked <- err
	}()
	select {
	case err := <-checked:
		t.Fatal("recovery bypassed in-flight renewal lock", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-checked; !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale discovery retained cleanup authority after renewal", err)
	}
	if rows, err := store.ListEnvironmentQualificationExecutionsForRecovery(t.Context(), placement.NodeID, "", 1); err != nil || len(rows) != 0 {
		t.Fatal("renewed lease remained in recovery discovery", len(rows), err)
	}
}
