package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/state"
)

func setupDestinationService(t *testing.T, store *PostgresStore, pool *pgxpool.Pool, account string) (*datapipeline.Service, api.DataConnection) {
	t.Helper()
	service := capacityPipelineService(t, pool)
	service.PlanMirror = func(context.Context, string, string) (datapipeline.MirrorPlan, error) {
		return datapipeline.MirrorPlan{}, nil
	}
	service.EnsureMirror = func(context.Context, datapipeline.Setup) error { return nil }
	service.MirrorState = func(ctx context.Context, account, id string) (string, error) {
		d, err := store.GetCustomerDatabase(ctx, account, id)
		if errors.Is(err, ErrNotFound) {
			return "", datapipeline.ErrNotFound
		}
		return string(d.State), err
	}
	c, err := service.CreateConnection(t.Context(), account, api.CreateDataConnectionRequest{Name: "setup-source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	passingCapacityCheck(t, service, account, c)
	return service, c
}

func destinationSetup(account string, c api.DataConnection, database, name string) datapipeline.Setup {
	return datapipeline.Setup{ID: uuid.NewString(), AccountID: account, DatabaseID: database, MirrorName: name, Request: datapipeline.SetupRequest{Name: name, SourceConnectionID: c.ID, DatabaseID: database, Tables: []string{"orders"}, ReadSubjects: []string{"alice"}}}
}

func TestSetupDestinationRejectsForeignMissingAndRetiringTargets(t *testing.T) {
	store, pool, ctx, victim := postgresStoreFixture(t)
	database := postgresReadyDatabase(t, store, victim, "victim-mirror", time.Now().UTC())
	attacker, err := state.NewPgStore(pool).CreateAccount(ctx, uuid.NewString()+"@setup.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	service, source := setupDestinationService(t, store, pool, attacker.ID)
	setups := service.Store.(datapipeline.SetupStore)
	if _, err = store.Get(ctx, attacker.ID, database.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign database is visible", err)
	}
	request := destinationSetup(attacker.ID, source, database.ID, "foreign-target")
	if _, err = service.StartSetup(ctx, attacker.ID, request.Request); !errors.Is(err, datapipeline.ErrNotFound) {
		t.Fatal("HTTP intent accepted a foreign target", err)
	}
	for _, explicit := range []bool{true, false} {
		job := request
		if !explicit {
			job.Request.DatabaseID = ""
		}
		if _, err = setups.EnqueueSetup(ctx, job); !errors.Is(err, datapipeline.ErrConflict) {
			t.Fatal("catalog writer reserved a foreign destination", explicit, err)
		}
	}
	missing := destinationSetup(attacker.ID, source, uuid.NewString(), "missing-target")
	if _, err = setups.EnqueueSetup(ctx, missing); !errors.Is(err, datapipeline.ErrConflict) {
		t.Fatal("catalog writer reserved a missing existing destination", err)
	}
	if jobs, err := setups.Setups(ctx, attacker.ID); err != nil || len(jobs) != 0 {
		t.Fatal("rejected admission left a reservation", jobs, err)
	}
	now := time.Now().UTC()
	if _, err = store.ClaimDelete(ctx, victim, database.ID, uuid.NewString(), now, now.Add(time.Minute)); err != nil {
		t.Fatal("foreign attempts affected the owner's delete claim", err)
	}
	retiring := postgresReadyDatabase(t, store, attacker.ID, "retiring-target", now)
	if _, err = store.ClaimDelete(ctx, attacker.ID, retiring.ID, uuid.NewString(), now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	job := destinationSetup(attacker.ID, source, retiring.ID, "retiring-setup")
	if _, err = service.StartSetup(ctx, attacker.ID, job.Request); !errors.Is(err, datapipeline.ErrConflict) {
		t.Fatal("service accepted a retiring destination", err)
	}
	if _, err = setups.EnqueueSetup(ctx, job); !errors.Is(err, datapipeline.ErrConflict) {
		t.Fatal("catalog writer accepted a retiring destination", err)
	}
	// New mirrors reserve a fresh ID before the database row exists.
	job = destinationSetup(attacker.ID, source, uuid.NewString(), "new-mirror")
	job.Request.DatabaseID = ""
	if _, err = setups.EnqueueSetup(ctx, job); err != nil {
		t.Fatal("ownership guard blocked new-mirror reservation", err)
	}
}

func waitForSetupDestinationLock(parent context.Context, t *testing.T, pool *pgxpool.Pool, blocker int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::integer=ANY(pg_blocking_pids(pid)))`, blocker).Scan(&waiting); err != nil {
			t.Fatal("destination operation did not acquire the expected lock", err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSetupDestinationSerializesAdmissionAndDeletion(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	service, source := setupDestinationService(t, store, pool, account)
	setups := service.Store.(datapipeline.SetupStore)
	for _, first := range []string{"delete", "setup"} {
		database := postgresReadyDatabase(t, store, account, "race-"+first, time.Now().UTC())
		t.Run(first+" wins", func(t *testing.T) {
			job := destinationSetup(account, source, database.ID, "setup-"+first)
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
			var pid int
			if err = tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			op, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if first == "delete" {
				if _, err = tx.Exec(ctx, `SELECT id FROM managed_postgres_databases WHERE id=$1 FOR UPDATE`, database.ID); err != nil {
					t.Fatal(err)
				}
				go func() { _, err := setups.EnqueueSetup(op, job); result <- err }()
				waitForSetupDestinationLock(op, t, pool, pid)
				if _, err = tx.Exec(ctx, `UPDATE managed_postgres_databases SET state='deleting' WHERE id=$1`, database.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				request, _ := json.Marshal(job.Request)
				if _, err = tx.Exec(ctx, `INSERT INTO data_pipeline_setups(id,account_id,source_connection_id,name,request,mirror_plan,database_id,mirror_name) VALUES($1,$2,$3,$4,$5,'{}',$6,$4)`, job.ID, account, source.ID, job.Request.Name, request, database.ID); err != nil {
					t.Fatal(err)
				}
				go func() {
					now := time.Now().UTC()
					_, err := store.ClaimDelete(op, account, database.ID, uuid.NewString(), now, now.Add(time.Minute))
					result <- err
				}()
				waitForSetupDestinationLock(op, t, pool, pid)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-result; err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("concurrent operation did not reject the conflicting reservation", err)
			}
		})
	}
}

func TestSetupDestinationMigrationRepairsForeignReservationsAndReplays(t *testing.T) {
	store, pool, ctx, victim := postgresStoreFixture(t)
	database := postgresReadyDatabase(t, store, victim, "legacy-victim", time.Now().UTC())
	attacker, err := state.NewPgStore(pool).CreateAccount(ctx, uuid.NewString()+"@legacy-setup.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	service, source := setupDestinationService(t, store, pool, attacker.ID)
	setups := service.Store.(datapipeline.SetupStore)
	ownerService, ownerSource := setupDestinationService(t, store, pool, victim)
	ownerDatabase := postgresReadyDatabase(t, store, victim, "legacy-owned", time.Now().UTC())
	legitimate, err := ownerService.Store.(datapipeline.SetupStore).EnqueueSetup(ctx, destinationSetup(victim, ownerSource, ownerDatabase.ID, "legacy-owned-setup"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261104105500001_pipeline_setup_destination_ownership.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing migration rollback")
	}
	if _, err = pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	job := destinationSetup(attacker.ID, source, database.ID, "legacy-foreign")
	job.ID = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	old, err := setups.EnqueueSetup(ctx, job)
	if err != nil {
		t.Fatal("could not reproduce an older worker's reservation", err)
	}
	// Put the invalid reservation beyond the first keyset batch. Historical
	// cancelled rows may share its source and must remain untouched.
	if _, err = pool.Exec(ctx, `INSERT INTO data_pipeline_setups(id,account_id,source_connection_id,name,request,mirror_plan,database_id,mirror_name,state)
	 SELECT ('00000000-0000-4000-8000-'||lpad(n::text,12,'0'))::uuid,account_id,source_connection_id,'history-'||n,request,mirror_plan,database_id,mirror_name,'cancelled'
	 FROM data_pipeline_setups CROSS JOIN generate_series(1,1000) n WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE data_pipeline_setups SET lease_token=$2,lease_until=clock_timestamp()+interval '1 hour' WHERE id=$1`, old.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	old, err = setups.Setup(ctx, attacker.ID, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	repair, err := migrations.FS.ReadFile("20261104105500002_pipeline_setup_destination_repair.sql")
	if err != nil {
		t.Fatal(err)
	}
	repairUp, repairDown, ok := strings.Cut(string(repair), "-- +goose Down")
	if !ok {
		t.Fatal("missing repair rollback")
	}
	for range 2 {
		if _, err = pool.Exec(ctx, up); err != nil {
			t.Fatal("ownership migration did not replay", err)
		}
		if _, err = pool.Exec(ctx, repairUp); err != nil {
			t.Fatal("batched repair did not replay", err)
		}
	}
	if _, err = pool.Exec(ctx, repairDown); err != nil {
		t.Fatal("repair rollback failed", err)
	}
	var unchanged int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM data_pipeline_setups WHERE name LIKE 'history-%' AND state='cancelled' AND revision=1`).Scan(&unchanged); err != nil || unchanged != 1000 {
		t.Fatal("repair changed historical reservations", unchanged, err)
	}
	repaired, err := setups.Setup(ctx, attacker.ID, old.ID)
	if err != nil || repaired.State != "cancelled" || repaired.Revision != old.Revision+1 || repaired.DatabaseID != old.DatabaseID || repaired.LeaseToken != "" {
		t.Fatal("migration lost or reactivated the legacy intent", repaired, err)
	}
	retained, err := setups.Setup(ctx, victim, legitimate.ID)
	if err != nil || retained.State != "working" || retained.Revision != legitimate.Revision || retained.DatabaseID != legitimate.DatabaseID {
		t.Fatal("migration altered legitimate owned work", retained, err)
	}
	if err = setups.FinishSetup(ctx, old, old); !errors.Is(err, datapipeline.ErrConflict) {
		t.Fatal("old worker completed a repaired reservation", err)
	}
	now := time.Now().UTC()
	if _, err = store.ClaimDelete(ctx, victim, database.ID, uuid.NewString(), now, now.Add(time.Minute)); err != nil {
		t.Fatal("legacy foreign reservation still blocks its victim", err)
	}
	if _, err = setups.ControlSetup(ctx, attacker.ID, old.ID, old.Revision, "retry"); !errors.Is(err, datapipeline.ErrConflict) {
		t.Fatal("stale worker reactivated a repaired reservation", err)
	}
}
