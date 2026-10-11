// adr: 590
package connectionfence

import (
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence/sqlc"
)

// TestMaintenanceIdentitySeparatesServerProcessesFromSessions reproduces the CI and
// production failure where an autovacuum worker in the maintenance database
// made every fence operation return ErrUnsupported. pg_stat_activity shows
// such a worker without an assigned role OID. A client whose role was dropped
// still has that OID even though its name is NULL. The query runs against a
// shadowing pg_stat_activity inside a rolled-back transaction to pin both cases.
func TestMaintenanceIdentitySeparatesServerProcessesFromSessions(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	for _, tc := range []struct {
		name    string
		usename string // "" inserts NULL
		hasRole bool
		private bool
	}{
		{name: "autovacuum worker", usename: "", private: true},
		{name: "maintenance role session", usename: f.admin, hasRole: true, private: true},
		{name: "infrastructure superuser session", usename: f.bootstrap.ConnConfig.User, hasRole: true, private: false},
		{name: "tenant session", usename: f.tenant, hasRole: true, private: false},
		{name: "dropped role session", usename: "", hasRole: true, private: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := f.maintenance.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			var usename any
			if tc.usename != "" {
				usename = tc.usename
			}
			if _, err := tx.Exec(ctx, "CREATE SCHEMA shadow_activity; CREATE TABLE shadow_activity.pg_stat_activity (datid oid, usename name, usesysid oid); SET LOCAL search_path=shadow_activity,pg_catalog"); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, "INSERT INTO shadow_activity.pg_stat_activity SELECT oid, $1::name, CASE WHEN $2::boolean THEN 1::oid END FROM pg_catalog.pg_database WHERE datname=current_database()", usename, tc.hasRole); err != nil {
				t.Fatal(err)
			}
			row, err := sqlc.New().MaintenanceIdentity(ctx, tx)
			if err != nil {
				t.Fatal(err)
			}
			if row.PrivateSessions != tc.private {
				t.Fatalf("private_sessions = %v with one %s row (usename %s), want %v",
					row.PrivateSessions, tc.name, pgx.Identifier{tc.usename}.Sanitize(), tc.private)
			}
		})
	}
}
