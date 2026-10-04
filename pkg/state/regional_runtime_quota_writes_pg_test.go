//go:build !no_pg

package state

// adr: 491
import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pgQuotaWriteFixture(t *testing.T) quotaWriteFixture {
	t.Helper()
	s, _, cell, account := releasePGFixture(t)
	return newQuotaWriteFixture(t, s, account, cell)
}

func TestPGRegionalQuotaWriteFences(t *testing.T) {
	runRegionalQuotaWriteContract(t, pgQuotaWriteFixture)
}

func quotaWriteMigration(t *testing.T) (string, string) {
	t.Helper()
	b, err := migrations.FS.ReadFile("20261004170000001_regional_runtime_quota_write_fences.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(b), "-- +goose Down")
	if !ok {
		t.Fatal("write fence migration missing downgrade")
	}
	return up, down
}

func TestPGRegionalQuotaWriteFenceMigration(t *testing.T) {
	f := pgQuotaWriteFixture(t)
	s := f.store.(*PgStore)
	up, down := quotaWriteMigration(t)
	for _, statement := range []string{down, up} {
		if _, err := s.pool.Exec(t.Context(), statement); err != nil {
			t.Fatalf("empty round trip: %v", err)
		}
	}
	f.freeze(t)
	if _, err := s.pool.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "cannot downgrade populated") {
		t.Fatalf("populated downgrade: %v", err)
	}
	if _, err := s.CreateInstance(t.Context(), f.quota.grant.Binding.RegionalAppID, f.deployment.ID, string(StateWarm), 128, f.node.ID, uuid.NewString()); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
		t.Fatalf("downgrade removed fence: %v", err)
	}
}

func TestPGRegionalQuotaWriteFenceDirectSQL(t *testing.T) {
	f := pgQuotaWriteFixture(t)
	s := f.store.(*PgStore)
	i := f.instance(t, StateRunning)
	base := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	intent := f.task(t, base, nil)
	claim, err := s.ClaimNextAppTask(t.Context(), "quota-worker", base.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.freeze(t)
	for _, statement := range []string{
		`UPDATE instances SET id=gen_random_uuid() WHERE id=$1`,
		`UPDATE instances SET wake_id=gen_random_uuid() WHERE id=$1`,
		`UPDATE instances SET netns='changed-quota-netns' WHERE id=$1`,
		`UPDATE instances SET ram_mb=ram_mb+1 WHERE id=$1`,
		`UPDATE instances SET app_id=NULL WHERE id=$1`,
		`UPDATE instances SET mode='mirror' WHERE id=$1`,
	} {
		if _, err := s.pool.Exec(t.Context(), statement, i.ID); !errors.Is(mapErr(err), ErrRegionalRuntimeQuotaFrozen) {
			t.Fatalf("SQL bypass %s: %v", statement, err)
		}
	}
	for _, statement := range []string{
		`UPDATE app_tasks SET id=gen_random_uuid() WHERE id=$1`,
		`UPDATE app_tasks SET status='running' WHERE id=$1`,
		`UPDATE app_tasks SET lease_token=gen_random_uuid() WHERE id=$1`,
		`UPDATE app_tasks SET command=ARRAY['bin/other'] WHERE id=$1`,
	} {
		if _, err := s.pool.Exec(t.Context(), statement, intent.ID); !errors.Is(mapErr(err), ErrRegionalRuntimeQuotaFrozen) {
			t.Fatalf("task SQL bypass %s: %v", statement, err)
		}
	}
	if _, err = s.pool.Exec(t.Context(), `INSERT INTO instances(app_id,deployment_id,state,ram_mb,node_id) VALUES($1,$2,'warm',128,$3)`, i.AppID, f.deployment.ID, f.node.ID); !errors.Is(mapErr(err), ErrRegionalRuntimeQuotaFrozen) {
		t.Fatalf("direct creation: %v", err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE instances SET request_count=request_count+1,last_request_at=now() WHERE id=$1`, i.ID); err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	if _, err = s.pool.Exec(t.Context(), `UPDATE instances SET state='draining' WHERE id=$1`, i.ID); err != nil {
		t.Fatalf("drain: %v", err)
	}
	stored, err := s.AppTaskByID(t.Context(), f.account.ID, intent.AppID, intent.ID)
	if err != nil || stored.Status != AppTaskRestoring || stored.LeaseToken == nil || *stored.LeaseToken != *claim.LeaseToken {
		t.Fatalf("SQL refusal changed task: %+v %v", stored, err)
	}
	// A retirement arriving before its grant must install the same write fence.
	other := newQuotaWriteFixture(t, s, f.account, f.quota.grant.Binding.CellID)
	_, envelope := other.quota.retirement(t, other.quota.grant)
	if _, err = other.quota.authority.AcceptRetirement(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateInstance(t.Context(), other.quota.grant.Binding.RegionalAppID, other.deployment.ID, string(StateColdBooting), 128, other.node.ID, uuid.NewString()); !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
		t.Fatalf("retirement-first creation: %v", err)
	}
}

func waitQuotaWriteBlocked(t *testing.T, pool *pgxpool.Pool, blocker int, query string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var blocked bool
		err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%'||$2||'%')`, blocker, query).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("runtime write did not wait on quota journal")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPGRegionalQuotaWriteFenceJournalLockWait(t *testing.T) {
	for _, action := range []string{"instance", "task-running", "task-claim"} {
		for _, commit := range []bool{true, false} {
			name := action + "/rollback"
			if commit {
				name = action + "/commit"
			}
			t.Run(name, func(t *testing.T) {
				f := pgQuotaWriteFixture(t)
				s := f.store.(*PgStore)
				base := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
				var task, claim AppTask
				var err error
				if action != "instance" {
					task = f.task(t, base, nil)
					if action == "task-running" {
						claim, err = s.ClaimNextAppTask(t.Context(), "quota-worker", base.Add(time.Second), time.Minute)
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				tx, err := s.pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(t.Context()) }()
				var pid int
				if err = tx.QueryRow(t.Context(), "SELECT pg_backend_pid()").Scan(&pid); err != nil {
					t.Fatal(err)
				}
				// Stage a signed fixture receipt through the same SQLC insert and
				// journal triggers used by acceptance, leaving its head uncommitted.
				envelope := f.quota.grantEnvelope(t, f.quota.grant)
				params, err := regionalQuotaParams(RegionalRuntimeQuotaReceipt{Grant: f.quota.grant, GrantEnvelope: &envelope})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = sqlc.New().InsertRegionalRuntimeQuotaReceipt(t.Context(), tx, params); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				result := make(chan error, 1)
				query := "update app_tasks"
				if action == "instance" {
					query = "insert into instances"
				}
				if action == "task-claim" {
					query = "ClaimAppTaskWithRegionalQuota"
				}
				go func() {
					var writeErr error
					if action == "instance" {
						_, writeErr = s.CreateInstance(ctx, f.quota.grant.Binding.RegionalAppID, f.deployment.ID, string(StateColdBooting), 128, f.node.ID, uuid.NewString())
					} else if action == "task-running" {
						_, writeErr = s.MarkAppTaskRunning(ctx, task.ID, *claim.LeaseToken, base.Add(2*time.Second))
					} else {
						_, writeErr = s.ClaimNextAppTask(ctx, "quota-worker", base.Add(time.Second), time.Minute)
					}
					result <- writeErr
				}()
				waitQuotaWriteBlocked(t, s.pool, pid, query)
				if commit {
					err = tx.Commit(t.Context())
				} else {
					err = tx.Rollback(t.Context())
				}
				if err != nil {
					t.Fatal(err)
				}
				err = <-result
				if commit && !errors.Is(err, ErrRegionalRuntimeQuotaFrozen) {
					t.Fatalf("committed grant missed after lock wait: %v", err)
				}
				if !commit && err != nil {
					t.Fatalf("rolled-back grant froze write: %v", err)
				}
				if action == "instance" {
					rows, readErr := s.ListInstancesForApp(t.Context(), f.quota.grant.Binding.RegionalAppID)
					want := 1
					if commit {
						want = 0
					}
					if readErr != nil || len(rows) != want {
						t.Fatalf("atomic instance result: %+v %v", rows, readErr)
					}
				} else {
					stored, readErr := s.AppTaskByID(t.Context(), f.account.ID, task.AppID, task.ID)
					want := AppTaskRunning
					if action == "task-claim" {
						want = AppTaskRestoring
					}
					if commit {
						want = AppTaskRestoring
						if action == "task-claim" {
							want = AppTaskQueued
						}
					}
					if readErr != nil || stored.Status != want {
						t.Fatalf("atomic task result: %+v %v", stored, readErr)
					}
				}
			})
		}
	}
}

func TestPGRegionalQuotaWriteFenceFixedSnapshot(t *testing.T) {
	f := pgQuotaWriteFixture(t)
	s := f.store.(*PgStore)
	for _, isolation := range []string{"REPEATABLE READ", "SERIALIZABLE"} {
		t.Run(isolation, func(t *testing.T) {
			tx, err := s.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(t.Context()) }()
			// The two fixed strings are test constants, never user input.
			if _, err = tx.Exec(t.Context(), "SET TRANSACTION ISOLATION LEVEL "+isolation); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(t.Context(), "SELECT 1 FROM regional_runtime_quota_heads"); err != nil {
				t.Fatal(err)
			}
			f.freeze(t)
			if _, err = tx.Exec(t.Context(), `INSERT INTO instances(app_id,deployment_id,state,ram_mb,node_id) VALUES($1,$2,'warm',128,$3)`, f.quota.grant.Binding.RegionalAppID, f.deployment.ID, f.node.ID); !errors.Is(mapErr(err), ErrRegionalRuntimeQuotaFrozen) {
				t.Fatalf("stale snapshot admitted: %v", err)
			}
		})
	}
}
