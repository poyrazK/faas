package state_test

// adr: 835

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state"
)

// Production-us rc.251: lifecycle_successor_apps fired on every park/wake
// status flip and locked the account FOR UPDATE while the wake held the app
// row. Request-ID journal inserts lock the account FOR KEY SHARE (FK) and then
// the app, so 150 concurrent requests to a parked app deadlocked the wake
// (SQLSTATE 40P01) and every request failed with 500 or 504.

func seedLifecycleApproval(t *testing.T, pool *pgxpool.Pool, s state.Store, accountID, appID string) string {
	t.Helper()
	var deployments []string
	for range 2 {
		d, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		deployments = append(deployments, d.ID)
	}
	id := uuid.NewString()
	if _, err := pool.Exec(t.Context(), `
		insert into route_lifecycle_approvals
			(id, account_id, app_id, baseline_deployment_id, candidate_deployment_id, receipt, approved_at, valid_until)
		values ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, '{"mappings":[]}'::jsonb, now(), now() + interval '1 hour')
	`, id, accountID, appID, deployments[0], deployments[1]); err != nil {
		t.Fatal(err)
	}
	return id
}

func lifecycleApprovalInvalidated(t *testing.T, pool *pgxpool.Pool, id string) bool {
	t.Helper()
	var invalidated bool
	if err := pool.QueryRow(t.Context(), `select invalidated_at is not null from route_lifecycle_approvals where id = $1::uuid`, id).Scan(&invalidated); err != nil {
		t.Fatal(err)
	}
	return invalidated
}

func TestLifecycleApprovalSurvivesScaleToZeroStatusFlips(t *testing.T) {
	pool, s := openHealthLockOrderStore(t)
	account, app := healthNotificationFixture(t, s)
	approval := seedLifecycleApproval(t, pool, s, account.ID, app.ID)
	for _, status := range []state.AppStatus{state.AppEvictedCold, state.AppActive, state.AppEvictedCold, state.AppActive} {
		if _, err := pool.Exec(t.Context(), `update apps set status = $2 where id = $1::uuid`, app.ID, string(status)); err != nil {
			t.Fatal(err)
		}
	}
	if lifecycleApprovalInvalidated(t, pool, approval) {
		t.Fatal("a park/wake status flip invalidated a route lifecycle approval")
	}
	if _, err := pool.Exec(t.Context(), `update apps set visibility = 'internal' where id = $1::uuid`, app.ID); err != nil {
		t.Fatal(err)
	}
	if !lifecycleApprovalInvalidated(t, pool, approval) {
		t.Fatal("a visibility change did not invalidate the route lifecycle approval")
	}
}

func TestLifecycleSuccessorTriggerDoesNotBlockForeignKeyChecks(t *testing.T) {
	pool, s := openHealthLockOrderStore(t)
	account, app := healthNotificationFixture(t, s)
	seedLifecycleApproval(t, pool, s, account.ID, app.ID)

	// A request-ID journal insert holds the FK key-share lock on the account.
	journal, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Rollback(context.Background()) })
	if _, err := journal.Exec(t.Context(), `select 1 from accounts where id = $1::uuid for key share`, account.ID); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	wake, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wake.Rollback(context.Background()) }()
	if _, err := wake.Exec(ctx, `set local lock_timeout = '2s'`); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.Exec(ctx, `select 1 from apps where id = $1::uuid for update`, app.ID); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`update apps set status = 'evicted_cold' where id = $1::uuid`,
		`update apps set status = 'active' where id = $1::uuid`,
		// A real lifecycle change still takes the account lock, in a mode
		// that does not conflict with foreign-key checks.
		`update apps set maintenance_mode = not maintenance_mode where id = $1::uuid`,
	} {
		if _, err := wake.Exec(ctx, stmt, app.ID); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
				t.Fatalf("%q waited on the account row held FOR KEY SHARE by a concurrent FK check: %v", stmt, err)
			}
			t.Fatalf("%q: %v", stmt, err)
		}
	}
	if err := wake.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
