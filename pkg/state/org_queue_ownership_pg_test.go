//go:build !no_pg

package state_test

// adr: 202610171908

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresOrgQueueOwnership(t *testing.T) {
	orgQueueOwnership(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func TestPostgresOrgQueueAdoption(t *testing.T) {
	orgQueueAdoption(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func TestPostgresOrgStageQueueOwnership(t *testing.T) {
	orgStageQueueOwnership(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPostgresOrgQueueCompanyCap(t *testing.T) {
	orgQueueCompanyCap(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPostgresOrgQueueTriggerQuota(t *testing.T) {
	orgQueueTriggerQuota(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func TestPostgresOrgQueueExternalTriggerGuard(t *testing.T) {
	orgQueueExternalTriggerGuard(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPostgresOrgQueueBackfill(t *testing.T) {
	for _, scenario := range []string{"retained roots", "mixed queue root", "inconsistent quota"} {
		t.Run(scenario, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			store := state.NewPgStore(pool)
			f := setupOffboarding(t, store)
			adoptObjectCompany(t, f)
			raw, err := migrations.FS.ReadFile("20261017190000007_org_queue_ownership.sql")
			if err != nil {
				t.Fatal(err)
			}
			up, down, ok := strings.Cut(string(raw), "-- +goose Down")
			if !ok {
				t.Fatal("missing rollback")
			}
			if _, err := pool.Exec(t.Context(), down); err != nil {
				t.Fatal(err)
			}
			binding, inv := uuid.NewString(), uuid.NewString()
			namespace := f.actor.ID
			if scenario == "mixed queue root" {
				namespace = f.owner.ID
			}
			if _, err := pool.Exec(t.Context(), `INSERT INTO queue_bindings(id,account_id,app_id,name,queue_name,mode,workload_class,enabled,max_concurrency) VALUES($1,$2,$3,'jobs','jobs','pull','worker',true,5)`, binding, namespace, f.app.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `INSERT INTO invocations(id,account_id,app_id,source,state,quota_reserved,attempts) VALUES($1,$2,$3,'queue','dispatching',true,1)`, inv, f.actor.ID, f.app.ID); err != nil {
				t.Fatal(err)
			}
			current := 2
			if scenario == "inconsistent quota" {
				current = 0
			}
			if _, err := pool.Exec(t.Context(), `INSERT INTO account_async_quota(account_id,max_inflight,current_inflight) VALUES($1,10,$2)`, f.actor.ID, current); err != nil {
				t.Fatal(err)
			}
			_, err = pool.Exec(t.Context(), up)
			if scenario != "retained roots" {
				if err == nil || !strings.Contains(err.Error(), "remediate") {
					t.Fatalf("unsafe migration: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var org, subject string
			var oldCount, newCount int
			if err := pool.QueryRow(t.Context(), `SELECT org_id FROM queue_bindings WHERE id=$1`, binding).Scan(&org); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(t.Context(), `SELECT async_quota_subject_id FROM invocations WHERE id=$1`, inv).Scan(&subject); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(t.Context(), `SELECT current_inflight FROM account_async_quota WHERE account_id=$1`, f.actor.ID).Scan(&oldCount); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(t.Context(), `SELECT current_inflight FROM account_async_quota WHERE account_id=$1`, f.org.ID).Scan(&newCount); err != nil {
				t.Fatal(err)
			}
			if org != f.org.ID || subject != f.org.ID || oldCount != 1 || newCount != 1 {
				t.Fatalf("backfill ownership %s %s, counters %d %d", org, subject, oldCount, newCount)
			}
			if _, err := pool.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "drain") {
				t.Fatalf("unsafe reservation rollback: %v", err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE queue_bindings SET account_id=$2 WHERE id=$1`, binding, f.owner.ID); err == nil {
				t.Fatal("rewrote binding namespace")
			}
		})
	}
}
