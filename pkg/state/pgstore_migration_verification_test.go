// adr:1572 — resumable migration evidence over existing runtime owners.
//go:build !no_pg

package state_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/migrationverify"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgMigrationVerificationAtomicTaskReceiptAndRecovery(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app, dep := seedLiveDeploy(t, s, ctx, "migration-receipt")
	if err := s.SetDeploymentRootfs(ctx, dep, "/test/rootfs", "test/rootfs", 1); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	r := state.MigrationVerificationRecord{AccountID: account, AppID: app, Report: api.MigrationVerification{ID: uuid.NewString(), App: "pg-app", DeploymentID: dep, Scope: "default", Status: "pending", Phase: "dependencies", CreatedAt: now, ExpiresAt: now.Add(time.Minute), Request: api.CreateMigrationVerificationRequest{ID: uuid.NewString(), DeploymentID: dep, Checks: api.MigrationDependencyChecks{PostgresEnvKey: "DATABASE_URL"}, SmokePath: "/ready", ColdWake: true}, Checks: []api.MigrationVerificationCheck{}}}
	r.Report.Request.ID = r.Report.ID
	r.Report.Request.Checks.ServiceConnections = []api.MigrationServiceConnectionCheck{{EnvKey: "API_URL", Service: "api", DeploymentID: uuid.NewString()}}
	r, err := s.CreateMigrationVerification(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	task := state.CreateAppTaskParams{AccountID: account, AppID: app, DeploymentID: dep, RequireLiveDeployment: true, Kind: state.AppTaskKindManual, Command: api.MigrationDependenciesCommand(r.Report.Request.Checks), TimeoutSeconds: 30, MaxOutputBytes: 8192, CreatedAt: now}
	// A failed journal write must roll back the queued task as well.
	_, err = pool.Exec(ctx, `CREATE FUNCTION reject_migration_update() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'fixture journal failure'; END$$; CREATE TRIGGER reject_migration_update BEFORE UPDATE ON migration_verifications FOR EACH ROW EXECUTE FUNCTION reject_migration_update()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveMigrationVerification(ctx, r, r.Version, &task); err == nil || !strings.Contains(err.Error(), "fixture journal failure") {
		t.Fatalf("journal failure was not reached: %v", err)
	}
	rows, err := s.ListAppTasks(ctx, account, app, 20, 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("orphan task: %d %v", len(rows), err)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER reject_migration_update ON migration_verifications; DROP FUNCTION reject_migration_update()`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	successes := make(chan state.MigrationVerificationRecord, 8)
	for range 8 {
		wg.Go(func() {
			saved, err := s.SaveMigrationVerification(ctx, r, r.Version, &task)
			if err == nil {
				successes <- saved
			} else if !errors.Is(err, state.ErrConflict) {
				t.Errorf("save: %v", err)
			}
		})
	}
	wg.Wait()
	close(successes)
	count := 0
	for saved := range successes {
		count++
		if saved.Report.DependencyTaskID == "" {
			t.Error("task identity missing")
		}
	}
	if count != 1 {
		t.Fatalf("committed saves=%d", count)
	}
	loaded, err := s.GetMigrationVerification(ctx, account, app, r.Report.ID)
	if err != nil || loaded.Report.DependencyTaskID == "" {
		t.Fatalf("lost receipt: %+v %v", loaded, err)
	}
	if len(loaded.Report.Request.Checks.ServiceConnections) != 1 || loaded.Report.Request.Checks.ServiceConnections[0] != r.Report.Request.Checks.ServiceConnections[0] {
		t.Fatal("restart lost exact connection target")
	}
	rows, err = s.ListAppTasks(ctx, account, app, 20, 0)
	if err != nil || len(rows) != 1 || rows[0].ID != loaded.Report.DependencyTaskID || rows[0].StartDeadlineAt == nil {
		t.Fatalf("task journal/deadline: %+v %v", rows, err)
	}
	if _, err = s.GetMigrationVerification(ctx, uuid.NewString(), app, r.Report.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("account isolation: %v", err)
	}
	other := r
	other.Report.ID = uuid.NewString()
	if _, err = s.CreateMigrationVerification(ctx, other); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("parallel active run accepted: %v", err)
	}
}

func TestPgMigrationVerificationWorkerDiscoveryAndCancellationFence(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app, dep := seedLiveDeploy(t, s, ctx, "migration-worker")
	if err := s.SetDeploymentRootfs(ctx, dep, "/test/rootfs", "test/rootfs", 1); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	in := api.CreateMigrationVerificationRequest{ID: uuid.NewString(), DeploymentID: dep, Checks: api.MigrationDependencyChecks{PostgresEnvKey: "DATABASE_URL"}, SmokePath: "/ready"}
	r := state.MigrationVerificationRecord{AccountID: account, AppID: app, Report: api.MigrationVerification{ID: in.ID, App: "pg-app", DeploymentID: dep, Scope: "default", Status: "pending", Phase: "dependencies", CreatedAt: now.Add(-40 * 24 * time.Hour), ExpiresAt: now.Add(-time.Minute), Request: in, Checks: []api.MigrationVerificationCheck{}}}
	r, err := s.CreateMigrationVerification(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListDueMigrationVerifications(ctx, now, 32)
	if err != nil || len(rows) != 1 || rows[0].Report.ID != in.ID {
		t.Fatalf("expired pending work lost: %+v %v", rows, err)
	}
	other := r
	other.Report.ID = uuid.NewString()
	other.Report.Request.ID = other.Report.ID
	if _, err := s.CreateMigrationVerification(ctx, other); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("pending cleanup pruned or bypassed: %v", err)
	}
	r.LeaseUntil = now.Add(time.Minute)
	r, err = s.SaveMigrationVerification(ctx, r, r.Version, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListDueMigrationVerifications(ctx, now, 32)
	if err != nil || len(rows) != 0 {
		t.Fatalf("active lease rediscovered: %+v %v", rows, err)
	}
	stale := r
	b := migrationverify.Backend{Store: s, AccountID: account, AppID: app}
	if _, err := b.Cancel(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	task := state.CreateAppTaskParams{AccountID: account, AppID: app, DeploymentID: dep, RequireLiveDeployment: true, Kind: state.AppTaskKindManual, Command: api.MigrationDependenciesCommand(in.Checks), TimeoutSeconds: 30, MaxOutputBytes: 8192, CreatedAt: now}
	if _, err := s.SaveMigrationVerification(ctx, stale, stale.Version, &task); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("late task escaped cancellation fence: %v", err)
	}
	tasks, err := s.ListAppTasks(ctx, account, app, 20, 0)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("orphan cancellation task: %+v %v", tasks, err)
	}
	// A new store represents another apid process. Cancellation and its old
	// lease must both survive; cleanup becomes due when that lease expires.
	restarted := state.NewPgStore(pool)
	r, err = restarted.GetMigrationVerification(ctx, account, app, in.ID)
	if err != nil || r.Report.CancelRequestedAt == nil || !r.LeaseUntil.Equal(stale.LeaseUntil) {
		t.Fatalf("restart lost cancellation/lease: %+v %v", r, err)
	}
	rows, err = restarted.ListDueMigrationVerifications(ctx, now.Add(2*time.Minute), 32)
	if err != nil || len(rows) != 1 {
		t.Fatalf("lease expiry did not recover work: %+v %v", rows, err)
	}
	r.Report.Status, r.Report.Phase = "cancelled", "complete"
	r.LeaseUntil = time.Time{}
	if _, err := restarted.SaveMigrationVerification(ctx, r, r.Version, nil); err != nil {
		t.Fatal(err)
	}
	rows, err = restarted.ListDueMigrationVerifications(ctx, now.Add(time.Hour), 32)
	if err != nil || len(rows) != 0 {
		t.Fatalf("terminal cleanup remained queued: %+v %v", rows, err)
	}
}
