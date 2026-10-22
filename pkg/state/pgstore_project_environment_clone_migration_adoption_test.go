//go:build !no_pg

// adr: 590
package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func cloneAdoptionDestination(t *testing.T, s *state.PgStore) string {
	t.Helper()
	n, err := s.CreateComputeNode(t.Context(), state.ComputeNode{Name: "clone-destination-" + uuid.NewString(), TargetURL: "tcp://10.0.0.2:7000", VPCPUs: 80, MemMB: 28000, MaxConcurrency: 100, AdmissionCeilingMB: 23800, VCPUBudget: api.VCPUSlots, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	return n.ID
}

func TestPgCloneMigrationAdoptionRetainsDestinationWithoutSourceMetadata(t *testing.T) {
	s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
	ctx := t.Context()
	in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	w := in.Plan.Workloads[0]
	source := resolveDefaultLocal(t, ctx, s)
	destination := cloneAdoptionDestination(t, s)
	ins, err := s.CreateInstance(ctx, w.AppID, w.TargetDeploymentID, string(state.StateRunning), 128, source, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	lease := state.MigrationLease{InstanceID: ins.ID, LeaseToken: uuid.NewString(), SourceNodeID: source, CreatedAt: time.Now().UTC().Truncate(time.Microsecond), LeaseExpiresAt: time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)}
	if err = s.ReserveMigrationLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkInstanceMigrating(ctx, ins.ID, source, lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	record := func(dest, mem, token string) error {
		return s.RecordCloneCheckpointMigrationAdoption(ctx, ins.ID, w.AppID, w.TargetDeploymentID, dest, mem, "original-state", token)
	}
	// Pending source snapshots do not grant destination adoption authority.
	if err = record(destination, "original-mem", lease.LeaseToken); err == nil {
		t.Fatal("pending source accepted")
	}
	if err = s.CompleteMigrationLease(ctx, lease.LeaseToken, "original-mem", "original-state"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct{ node, mem, token string }{{source, "original-mem", lease.LeaseToken}, {uuid.NewString(), "original-mem", lease.LeaseToken}, {destination, "foreign-mem", lease.LeaseToken}, {destination, "original-mem", uuid.NewString()}} {
		if err = record(bad.node, bad.mem, bad.token); err == nil {
			t.Fatal("foreign adoption authority accepted")
		}
	}
	if err = record(destination, "original-mem", lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err = record(destination, "original-mem", lease.LeaseToken); err == nil {
		t.Fatal("unknown destination redispatched")
	}
	if err = record(cloneAdoptionDestination(t, s), "original-mem", lease.LeaseToken); err == nil {
		t.Fatal("lease redirected after unknown adoption")
	}
	// Ordinary pre-fence source cleanup must not erase the independent request.
	if err = s.DeleteMigrationLease(ctx, lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteInstance(ctx, ins.ID); err != nil {
		t.Fatal(err)
	}
	var src, dst, dep, token, mem, vmstate, wake string
	if err = pool.QueryRow(ctx, "SELECT source_node_id,destination_node_id::text,deployment_id::text,lease_token,mem_storage_key,vmstate_storage_key,wake_id::text FROM project_environment_clone_migration_adoptions WHERE instance_id=$1", ins.ID).Scan(&src, &dst, &dep, &token, &mem, &vmstate, &wake); err != nil || src != source || dst != destination || dep != w.TargetDeploymentID || token != lease.LeaseToken || mem != "original-mem" || vmstate != "original-state" || wake != ins.WakeID {
		t.Fatal("original destination evidence lost", err)
	}
	for _, q := range []string{"DELETE FROM project_environment_clone_migration_adoptions", "UPDATE project_environment_clone_migration_adoptions SET destination_node_id=gen_random_uuid()", "TRUNCATE project_environment_clone_migration_adoptions"} {
		_, err = pool.Exec(ctx, q)
		var e *pgconn.PgError
		if !errors.As(err, &e) || e.ConstraintName != "clone_checkpoint_migration_adoption_retained" {
			t.Fatal("adoption record not retained", err)
		}
	}
	in, _, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, pgWorkloadRetirementProof(in.Plan)); !errors.Is(err, state.ErrConflict) {
		t.Fatal("destination uncertainty erased with source metadata", err)
	}
	saved, err := s.ProjectEnvironmentCloneCheckpointWorkloadRetirementForLease(ctx, l)
	if err != nil || saved.State != "requested" || saved.ObservationSHA256 != "" {
		t.Fatal("partial native receipt", err)
	}
	// Source-stage migration stays compatible and gets no clone dispatch row.
	other, err := s.CreateInstance(ctx, w.AppID, w.SourceDeploymentID, string(state.StateRunning), 128, source, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkInstanceMigrating(ctx, other.ID, source, "source-token"); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordCloneCheckpointMigrationAdoption(ctx, other.ID, w.AppID, w.SourceDeploymentID, destination, "source-mem", "source-state", "source-token"); err != nil {
		t.Fatal("source migration fenced", err)
	}
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM project_environment_clone_migration_adoptions").Scan(&count); err != nil || count != 1 {
		t.Fatal("source acquired clone dispatch evidence", err)
	}
	raw, err := migrations.FS.ReadFile("20261011232006429_clone_migration_adoption_dispatch.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, strings.SplitN(string(raw), "-- +goose Down", 2)[1])
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.ConstraintName != "clone_checkpoint_migration_adoption_down_no_evidence" {
		t.Fatal("unsafe downgrade", err)
	}
}

func TestPgCloneMigrationAdoptionQueuedAndStaleSnapshot(t *testing.T) {
	s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
	ctx := t.Context()
	in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	w := in.Plan.Workloads[0]
	source := resolveDefaultLocal(t, ctx, s)
	dest := cloneAdoptionDestination(t, s)
	ins, err := s.CreateInstance(ctx, w.AppID, w.TargetDeploymentID, string(state.StateRunning), 128, source, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if err = s.ReserveMigrationLease(ctx, state.MigrationLease{InstanceID: ins.ID, LeaseToken: token, SourceNodeID: source, CreatedAt: time.Now().UTC(), LeaseExpiresAt: time.Now().UTC().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteMigrationLease(ctx, token, "mem", "state"); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkInstanceMigrating(ctx, ins.ID, source, token); err != nil {
		t.Fatal(err)
	}
	old, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Rollback(ctx) }()
	if _, err = old.Exec(ctx, "SELECT state FROM project_environment_clone_checkpoint_workload_retirements"); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT lock_clone_checkpoint_scheduler_admission()"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- s.RecordCloneCheckpointMigrationAdoption(ctx, ins.ID, w.AppID, w.TargetDeploymentID, dest, "mem", "state", token)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%SELECT record_clone_checkpoint_migration_adoption%')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("adoption did not queue")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = tx.Exec(ctx, "UPDATE project_environment_clone_checkpoint_workload_retirements SET state='requested',requested_at=clock_timestamp() WHERE operation_id=$1", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		requireCloneSchedulerFence(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("adoption remained queued")
	}
	_, err = old.Exec(ctx, "SELECT record_clone_checkpoint_migration_adoption($1,$2,$3,$4,'mem','state',$5)", ins.ID, w.AppID, w.TargetDeploymentID, dest, token)
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.Code != "40001" {
		t.Fatal("stale adoption admitted", err)
	}
}

func TestPgCloneMigrationAdoptionEmptyMigrationReplay(t *testing.T) {
	_, pool, _ := pgWorkloadRetirementFixture(t, "unmaterialized")
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261011232006429_clone_migration_adoption_dispatch.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, q := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(ctx, q); err != nil {
			t.Fatal("empty migration replay", err)
		}
	}
}
