package migrations

import "testing"

func TestTargetNotificationMigrationFollowsRouteSchema(t *testing.T) {
	const (
		overflowSchema = "20260928173653293_managed_realtime_channel_route_channel_overflow.sql"
		revisions      = "20260929081244732_managed_realtime_channel_route_revisions.sql"
		notifications  = "20260929081244733_managed_realtime_channel_route_target_notifications.sql"
	)

	versions := make(map[string]int64, 3)
	for _, migration := range LoadMigrations(t) {
		if migration.Name == overflowSchema || migration.Name == revisions || migration.Name == notifications {
			versions[migration.Name] = migration.Version
		}
	}
	for _, name := range []string{overflowSchema, revisions, notifications} {
		if _, ok := versions[name]; !ok {
			t.Fatalf("migration %q is missing from the embedded migration set", name)
		}
	}
	if versions[notifications] <= versions[overflowSchema] || versions[notifications] <= versions[revisions] {
		t.Fatalf("target notification migration version %d must follow overflow schema version %d and route revision version %d", versions[notifications], versions[overflowSchema], versions[revisions])
	}
}
