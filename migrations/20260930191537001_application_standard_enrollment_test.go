//go:build !no_pg

// Regression test for 20260930191537001_application_standard_enrollment.sql.
//
// apps.org_id is nullable: the personal-org attribution repair
// (20260923190000001) skips an account without a personal org, so such an
// app can still have no owner when the enrollment migration runs. Its
// backfill must enroll owned apps only; app_application_standards.org_id is
// NOT NULL, and an unowned row aborted the whole upgrade.
package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrationApplicationStandardEnrollmentSkipsUnownedApps(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, 20260930190103001)

	ownerID := seedAccount(t, ctx, pool)
	orgID := seedPersonalOrg(t, ctx, pool, ownerID)
	ownedID := seedApp(t, ctx, pool, ownerID)
	if _, err := pool.Exec(ctx, `update apps set org_id = $1 where id = $2`, orgID, ownedID); err != nil {
		t.Fatal(err)
	}
	unownedID := seedApp(t, ctx, pool, seedAccount(t, ctx, pool))

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("upgrade with an unowned app: %v", err)
	}
	var enrolledOrg string
	if err := pool.QueryRow(ctx, `select org_id::text from app_application_standards where app_id = $1`, ownedID).Scan(&enrolledOrg); err != nil || enrolledOrg != orgID {
		t.Fatalf("owned app enrollment org = %q, %v; want %q", enrolledOrg, err, orgID)
	}
	var unowned int
	if err := pool.QueryRow(ctx, `select count(*) from app_application_standards where app_id = $1`, unownedID).Scan(&unowned); err != nil || unowned != 0 {
		t.Fatalf("unowned app enrollments = %d, %v; want none", unowned, err)
	}
}
