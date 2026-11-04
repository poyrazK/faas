//go:build !no_pg

// adr: 570
package state

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPgTrafficReleaseAdditionPublication(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := NewPgStore(pool, WithTrafficAppsDomain("apps.example.test"))
	testTrafficReleaseAdditionPublication(t, store, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string {
		var reservations string
		if err := pool.QueryRow(t.Context(), `SELECT md5(jsonb_build_object(
 'additions',(SELECT jsonb_agg(to_jsonb(t) ORDER BY app_id) FROM project_release_additions t),
 'submissions',(SELECT jsonb_agg(to_jsonb(t) ORDER BY attempt_id) FROM project_submissions t))::text)`).Scan(&reservations); err != nil {
			t.Fatal(err)
		}
		return pgTrafficAppBindingIntent(t, pool) + reservations
	})
}
