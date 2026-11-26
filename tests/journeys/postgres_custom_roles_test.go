package journeys

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestPostgresCustomRoles is the custom Postgres roles journey (row 109,
// beta): declare a role an app switches to and a BYPASSRLS role its
// read_write logins start as, grant them from a release command, read a
// row-level-secured table through the app as each, and delete them.
func TestPostgresCustomRoles(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	j := &postgresJourney{tg: tg, bindings: map[string]string{}}
	hidden := hiddenCapabilities(t, tg)
	skipHidden(t, hidden, "postgres-custom-roles")
	skipHidden(t, hidden, "release-commands")
	j.app = tg.app(t, "pgroles")
	j.db = tg.slug("pgroles")
	t.Cleanup(func() { j.purge(t, j.db) })
	s := newStory(t)
	s.step("postgres_available", j.available)
	s.step("create_database", func(t *testing.T) {
		tg.ok(t, time.Minute, "postgres", "create", j.db, "--region", j.region)
		j.waitReady(t, j.db)
	})
	s.step("declare_roles", func(t *testing.T) {
		tg.ok(t, time.Minute, "postgres", "roles", "create", j.db, "journey_user")
		tg.ok(t, time.Minute, "postgres", "roles", "create", j.db, "journey_admin",
			"--bypass-rls", "--member-of", "journey_user", "--default-for", "read_write")
		j.waitRoles(t, "journey_admin", "journey_user")
	})
	s.step("deploy_app", func(t *testing.T) {
		receipt := tg.deploy(t, j.app, "--path", postgresProbeSource(t, ""))
		j.base = strings.TrimRight(str(receipt, "app_url"), "/")
		if j.base == "" {
			j.base = tg.appURL(j.app)
		}
	})
	s.step("attach_database", func(t *testing.T) { j.attach(t, j.db, "DATABASE_URL") })
	s.step("migrate", func(t *testing.T) {
		// The release command creates the table, its policy and a function,
		// and grants the table to journey_user.
		id := j.bind(t, j.db, "MIGRATION_DATABASE_URL", "--access", "migration")
		tg.deploy(t, j.app, "--path", postgresProbeSource(t, "roles"), "--rollout", "immediate")
		j.unbind(t, j.db, id)
	})
	s.step("default_role", func(t *testing.T) {
		// read_write logins start as journey_admin, which bypasses RLS.
		j.expectRole(t, "", "journey_admin", 2)
	})
	s.step("set_role", func(t *testing.T) {
		// SET LOCAL ROLE journey_user: the policy shows alice's row only,
		// and the role runs the schema owner's function.
		j.expectRole(t, "journey_user", "journey_user", 1)
	})
	s.step("delete_roles", func(t *testing.T) {
		j.detach(t, j.db)
		tg.ok(t, time.Minute, "postgres", "roles", "delete", j.db, "journey_admin", "--yes")
		tg.ok(t, time.Minute, "postgres", "roles", "delete", j.db, "journey_user", "--yes")
		j.waitRoles(t)
	})
	s.step("purge", func(t *testing.T) { j.purgeNow(t, j.db) })
}

// waitRoles waits until the database's custom roles are exactly want, all
// ready.
func (j *postgresJourney) waitRoles(t *testing.T, want ...string) {
	t.Helper()
	eventually(t, "custom roles applied", 5*time.Minute, 5*time.Second, func() (bool, string) {
		list := object(t, j.tg.ok(t, time.Minute, "postgres", "roles", "list", j.db).stdout)
		items, _ := field(list, "items").([]any)
		if len(items) != len(want) {
			return false, compact(list)
		}
		for _, item := range items {
			if str(item, "state") != "ready" || !slices.Contains(want, str(item, "name")) {
				return false, compact(list)
			}
		}
		return true, ""
	})
}

// expectRole reads /roles as role ("" for the login's default) and checks
// the session's role, the function's answer and the rows visible to alice.
func (j *postgresJourney) expectRole(t *testing.T, role, wantUser string, wantRows int) {
	t.Helper()
	target := j.base + "/roles"
	if role != "" {
		target += "?role=" + role
	}
	var last httpResult
	eventually(t, "GET /roles "+role, 3*time.Minute, 5*time.Second, func() (bool, string) {
		last = send(http.MethodGet, target, nil, nil)
		return last.err == nil && last.status == http.StatusOK, last.String()
	})
	doc := jsonBody(t, last)
	if str(doc, "current_user") != wantUser || str(doc, "whoami") != wantUser || int(num(doc, "rows")) != wantRows {
		t.Fatalf("/roles as %q answered %s, want user %s and %d rows", role, compact(doc), wantUser, wantRows)
	}
}
