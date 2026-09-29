package migrations

import "testing"

func TestRouteRevisionMigrationFollowsCurrentMainline(t *testing.T) {
	const (
		mainlineTail = "20260929081244731_oidc_drop_permissive_trust_policies.sql"
		revisions    = "20260929081244732_managed_realtime_channel_route_revisions.sql"
	)

	versions := make(map[string]int64, 2)
	for _, migration := range LoadMigrations(t) {
		if migration.Name == mainlineTail || migration.Name == revisions {
			versions[migration.Name] = migration.Version
		}
	}
	for _, name := range []string{mainlineTail, revisions} {
		if _, ok := versions[name]; !ok {
			t.Fatalf("migration %q is missing from the embedded migration set", name)
		}
	}
	if versions[revisions] <= versions[mainlineTail] {
		t.Fatalf("route revision migration version %d must follow current mainline tail version %d", versions[revisions], versions[mainlineTail])
	}
}
