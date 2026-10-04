//go:build !no_pg

package globaldirectory

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func postgresQuotaFixture(t *testing.T, shared bool, regions int) (*pgxpool.Pool, quotaFixture) {
	t.Helper()
	pool := directoryPG(t)
	a := state.NewPgStore(pool)
	actor, err := a.CreateAccount(t.Context(), uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	return pool, seedQuotaFixture(t, NewPgStore(pool), a, actor, shared, regions)
}
func TestGlobalRuntimeQuotaPostgresContract(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "personal"
		if shared {
			name = "organization"
		}
		t.Run(name, func(t *testing.T) { _, f := postgresQuotaFixture(t, shared, 2); exerciseRuntimeQuotaContract(t, f) })
	}
}
func TestGlobalRuntimeQuotaPostgresPeakRace(t *testing.T) {
	_, f := postgresQuotaFixture(t, false, 1)
	exerciseQuotaPeakAndRace(t, f)
}
func TestGlobalRuntimeQuotaPostgresIssueRetireRace(t *testing.T) {
	_, f := postgresQuotaFixture(t, false, 1)
	exerciseQuotaIssueRetireRace(t, f)
}
func TestGlobalRuntimeQuotaPostgresAuthority(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "personal"
		if shared {
			name = "organization"
		}
		t.Run(name, func(t *testing.T) { _, f := postgresQuotaFixture(t, shared, 1); exerciseQuotaAuthority(t, f) })
	}
}

func TestGlobalRuntimeQuotaPostgresAuthorityWait(t *testing.T) {
	for _, kind := range []string{"personal-plan", "organization-plan", "role", "removal", "issue-plan"} {
		t.Run(kind, func(t *testing.T) {
			shared := kind != "personal-plan"
			pool, f := postgresQuotaFixture(t, shared, 1)
			if kind == "role" || kind == "removal" {
				actor, err := f.authority.CreateAccount(t.Context(), "quota-admin@example.com", api.PlanFree)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.authority.MarkAccountEmailVerified(t.Context(), actor.ID); err != nil {
					t.Fatal(err)
				}
				if err = f.authority.AddOrgMember(t.Context(), f.orgID, actor.ID, state.OrgRoleAdmin, nil); err != nil {
					t.Fatal(err)
				}
				f.request.ActorID = actor.ID
			}
			if kind == "issue-plan" {
				if _, err := f.quota.ReserveRuntimeQuota(t.Context(), f.request); err != nil {
					t.Fatal(err)
				}
			}
			tx := beginDirectoryTx(t, pool)
			lockSQL, tenant := "SELECT id FROM orgs WHERE id=$1 FOR UPDATE", f.orgID
			if !shared {
				lockSQL, tenant = "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", f.actor.ID
			}
			if _, err := tx.Exec(t.Context(), lockSQL, tenant); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				var err error
				if kind == "issue-plan" {
					_, err = f.quota.IssueRuntimeQuotaTarget(ctx, f.owner.ID, f.request.Slug, f.request.AllocationID, f.request.Shares[0].Region)
				} else {
					_, err = f.quota.ReserveRuntimeQuota(ctx, f.request)
				}
				result <- err
			}()
			query := "%reserve_global_runtime_quota%"
			if kind == "issue-plan" {
				query = "%lock_global_runtime_quota_tenant%"
			}
			blocked := false
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if err := pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1)", query).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !blocked {
				t.Fatal("quota operation did not reach the tenant lock")
			}
			changeSQL, args := "UPDATE orgs SET plan='hobby' WHERE id=$1", []any{f.orgID}
			want := ErrOwnerPlanChanged
			switch kind {
			case "personal-plan":
				changeSQL, args = "UPDATE accounts SET plan='hobby' WHERE id=$1", []any{f.actor.ID}
			case "role":
				changeSQL, args = "UPDATE org_memberships SET role='viewer' WHERE org_id=$1 AND account_id=$2", []any{f.orgID, f.request.ActorID}
				want = ErrOwnerForbidden
			case "removal":
				changeSQL, args = "UPDATE org_memberships SET removed_at=now() WHERE org_id=$1 AND account_id=$2", []any{f.orgID, f.request.ActorID}
				want = ErrOwnerForbidden
			}
			if _, err := tx.Exec(t.Context(), changeSQL, args...); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := <-result; !errors.Is(err, want) {
				t.Fatalf("quota accepted stale authority after waiting: %v", err)
			}
			var count int
			if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM global_runtime_quota_targets t JOIN global_apps a ON a.id=t.global_app_id WHERE a.owner_id=$1 AND (t.issued_at IS NOT NULL OR $2)", f.owner.ID, kind != "issue-plan").Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed operation left quota authority: count=%d err=%v", count, err)
			}
		})
	}
}

func quotaMigrationParts(t *testing.T) []string {
	t.Helper()
	m, err := os.ReadFile("../../migrations/20261004120000001_global_runtime_quota_reservations.sql")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(m), "-- +goose Down")
}

func TestGlobalRuntimeQuotaPostgresSQLGuards(t *testing.T) {
	pool, f := postgresQuotaFixture(t, true, 1)
	a, err := f.quota.ReserveRuntimeQuota(t.Context(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"UPDATE global_runtime_quota_targets SET concurrency=concurrency+1 WHERE global_app_id=$1",
		"UPDATE global_runtime_quota_allocations SET generation=generation+1 WHERE global_app_id=$1",
		"UPDATE global_runtime_quota_allocations SET max_concurrency=max_concurrency+1 WHERE global_app_id=$1",
		"UPDATE global_apps SET desired_runtime_quota_generation=0 WHERE id=$1",
		"DELETE FROM global_runtime_quota_targets WHERE global_app_id=$1",
		"DELETE FROM global_runtime_quota_allocations WHERE global_app_id=$1",
		"UPDATE global_runtime_quota_targets SET released_at=now() WHERE global_app_id=$1",
	} {
		if _, err = pool.Exec(t.Context(), q, a.GlobalAppID); err == nil {
			t.Fatalf("SQL quota bypass: %s", q)
		}
	}
	if _, err = pool.Exec(t.Context(), "UPDATE global_runtime_quota_policies SET max_concurrency=max_concurrency+1"); err == nil {
		t.Fatal("policy rewritten")
	}
	if _, err = pool.Exec(t.Context(), "TRUNCATE global_runtime_quota_targets"); err == nil {
		t.Fatal("target retention bypass")
	}
	if _, err = f.quota.IssueRuntimeQuotaTarget(t.Context(), f.owner.ID, f.request.Slug, a.AllocationID, f.request.Shares[0].Region); err != nil {
		t.Fatal(err)
	}
	if _, err = f.quota.RetireRuntimeQuota(t.Context(), f.owner.ID, f.actor.ID, f.request.Slug, a.AllocationID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), "UPDATE global_runtime_quota_targets SET released_at=now() WHERE global_app_id=$1", a.GlobalAppID); err == nil {
		t.Fatal("issued quota released without acknowledgement")
	}
	if _, err = pool.Exec(t.Context(), "DELETE FROM global_apps WHERE id=$1", a.GlobalAppID); err == nil {
		t.Fatal("app deletion released issued capacity")
	}
	parts := quotaMigrationParts(t)
	if _, err = pool.Exec(t.Context(), parts[1]); err == nil {
		t.Fatal("populated quota downgrade accepted")
	}
	if got, err := f.quota.RuntimeQuota(t.Context(), f.owner.ID, f.request.Slug, a.AllocationID); err != nil || got.Targets[0].ReleasedAt != nil {
		t.Fatalf("failed bypass changed charge: %+v %v", got, err)
	}
}

func TestGlobalRuntimeQuotaPostgresEmptyMigrationRoundTrip(t *testing.T) {
	pool := directoryPG(t)
	parts := quotaMigrationParts(t)
	if _, err := pool.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), parts[0]); err != nil {
		t.Fatal(err)
	}
	a := state.NewPgStore(pool)
	actor, err := a.CreateAccount(t.Context(), "quota-roundtrip@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f := seedQuotaFixture(t, NewPgStore(pool), a, actor, false, 1)
	if _, err = f.quota.ReserveRuntimeQuota(t.Context(), f.request); err != nil {
		t.Fatal(err)
	}
}
