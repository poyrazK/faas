package state

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgApplicationStandardObservationLifecycle(t *testing.T) {
	store, _ := runtimeCapturePGStore(t)
	standardObservationLifecycle(t, store)
}

func TestPgApplicationStandardObservationProviderLifecycle(t *testing.T) {
	store, _ := runtimeCapturePGStore(t)
	standardObservationProviderLifecycle(t, store)
}

func TestPgApplicationStandardObservationIdleSnapshots(t *testing.T) {
	store, _ := runtimeCapturePGStore(t)
	standardObservationIdleSnapshots(t, store)
}

func TestPgApplicationStandardObservationPauseFence(t *testing.T) {
	store, _ := runtimeCapturePGStore(t)
	standardObservationPauseFence(t, store)
}

func TestPgApplicationStandardObservationStoredWaveBit(t *testing.T) {
	store, pool := runtimeCapturePGStore(t)
	standardObservationStoredWaveBit(t, store, func(o ApplicationStandardOperation) {
		if _, err := pool.Exec(t.Context(), "UPDATE application_standard_operation_targets SET state='observed' WHERE operation_id=$1 AND position=0", o.ID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPgApplicationStandardObservationMembershipFence(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	ins, _ := issueConsumedNativeFixture(t, s)
	app, err := s.AppByID(t.Context(), ins.AppID)
	if err != nil {
		t.Fatal(err)
	}
	standardObservationLoadLogging(t, s, app)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := sqlc.New().LockApplicationStandardObservation(t.Context(), tx, sqlc.LockApplicationStandardObservationParams{AppID: mustPgUUID(app.ID), OrgID: mustPgUUID(app.OrgID)}); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlc.New().LockApplicationStandardObservationEvidence(t.Context(), tx, mustPgUUID(app.ID)); err != nil {
		t.Fatal(err)
	}
	// Existing row locks cannot fence a newly enrolled node. The membership
	// advisory fence must reject this phantom while observation is in progress.
	_, nodeErr := s.CreateComputeNode(t.Context(), ComputeNode{Name: "observer-phantom-" + uuid.NewString(), TargetURL: "unix:///tmp/observer-phantom", VPCPUs: 2, MemMB: 1024, MaxConcurrency: 5, AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true})
	standardObservationAssertPGLock(t, nodeErr)
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(t.Context(), "SET lock_timeout='100ms'"); err != nil {
		t.Fatal(err)
	}
	// Test-only raw SQL exercises the placement trigger, including insertion.
	_, placementErr := conn.Exec(t.Context(), "UPDATE instances SET state=state WHERE id=$1", ins.ID)
	standardObservationAssertPGLock(t, placementErr)
	_, reportErr := conn.Exec(t.Context(), "DELETE FROM application_standard_log_inventories WHERE app_id=$1", app.ID)
	standardObservationAssertPGLock(t, reportErr)
}

func standardObservationAssertPGLock(t *testing.T, err error) {
	t.Helper()
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "55P03" {
		t.Fatal("writer bypassed observer fencing or failed for another reason", err)
	}
}
