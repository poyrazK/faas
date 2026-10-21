//go:build !no_pg

// adr: 595. PostgreSQL and memory previews retain captured admission versions.
package state

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPgApplicationStandardPreviewSetOnboarding(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	standardPreviewSetOnboarding(t, NewPgStore(pool), func(a appstandards.Assignment, actor string) {
		_, err := pool.Exec(ctx, `INSERT INTO application_standard_assignments
			(id, org_id, scope, scope_id, standard_id, admission_version, active, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, true, $7)
			ON CONFLICT (id) DO UPDATE SET admission_version = EXCLUDED.admission_version,
			  revision = application_standard_assignments.revision + 1, updated_at = now()`,
			a.ID, a.OrgID, a.Scope, a.ScopeID, a.StandardID, a.AdmissionVersion, actor)
		if err != nil {
			t.Fatal(err)
		}
	}, func(accountID string, want int) {
		var enrollments, addresses, cursor int
		if err := pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM app_application_standards),
			(SELECT count(*) FROM apps WHERE account_id = $1 AND service_address_index IS NOT NULL),
			coalesce((SELECT last_index FROM app_service_address_cursors WHERE account_id = $1), 0)`, accountID).
			Scan(&enrollments, &addresses, &cursor); err != nil {
			t.Fatal(err)
		}
		if enrollments != want || addresses != want || cursor != want {
			t.Fatalf("preview inventory: enrollments=%d addresses=%d cursor=%d, want %d", enrollments, addresses, cursor, want)
		}
	})
}
