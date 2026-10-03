package migrations

import (
	"strings"
	"testing"
)

func TestScopedTargetInvalidationMigrationFollowsTargetNotifications(t *testing.T) {
	const (
		notifications = "20260929081244733_managed_realtime_channel_route_target_notifications.sql"
		scope         = "20260929081244734_managed_realtime_channel_route_target_invalidation_scope.sql"
	)

	versions := make(map[string]int64, 2)
	for _, migration := range LoadMigrations(t) {
		if migration.Name == notifications || migration.Name == scope {
			versions[migration.Name] = migration.Version
		}
	}
	for _, name := range []string{notifications, scope} {
		if _, ok := versions[name]; !ok {
			t.Fatalf("migration %q is missing from the embedded migration set", name)
		}
	}
	if versions[scope] <= versions[notifications] {
		t.Fatalf("scoped invalidation migration version %d must follow target notification version %d", versions[scope], versions[notifications])
	}
}

func TestScopedTargetInvalidationDropsTriggersBeforeRecreating(t *testing.T) {
	const migration = "20260929081244734_managed_realtime_channel_route_target_invalidation_scope.sql"
	contents, err := FS.ReadFile(migration)
	if err != nil {
		t.Fatalf("read embedded migration %q: %v", migration, err)
	}
	sql := strings.ToLower(string(contents))
	triggers := []string{
		"managed_realtime_channel_route_targets_routes_trg",
		"managed_realtime_channel_route_targets_node_state_write_trg",
		"managed_realtime_channel_route_targets_node_state_update_trg",
		"managed_realtime_channel_route_targets_generation_write_trg",
		"managed_realtime_channel_route_targets_generation_update_trg",
		"managed_realtime_channel_route_targets_overflow_write_trg",
		"managed_realtime_channel_route_targets_overflow_update_trg",
		"managed_realtime_channel_route_targets_overflow_channels_write_trg",
		"managed_realtime_channel_route_targets_overflow_channels_update_trg",
		"managed_realtime_channel_route_targets_compute_nodes_write_trg",
		"managed_realtime_channel_route_targets_compute_nodes_update_trg",
	}
	for _, trigger := range triggers {
		dropAt := strings.Index(sql, "drop trigger if exists "+trigger)
		createAt := strings.Index(sql, "create trigger "+trigger)
		if dropAt < 0 || createAt < 0 || dropAt > createAt {
			t.Errorf("migration %s must drop trigger %s before recreating it", migration, trigger)
		}
	}
}
