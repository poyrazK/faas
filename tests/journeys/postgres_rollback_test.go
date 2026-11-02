package journeys

import (
	"strings"
	"testing"
	"time"
)

// pgJourneysDatabases is how many databases the PostgreSQL journeys hold at
// once when they run together: TestManagedPostgres and
// TestManagedPostgresRollbackRestore two each (a database and its restored
// copy), TestStatelessAgent and the Data API journey one each.
const pgJourneysDatabases = 6

// pgSwitchWait bounds a rollback that restores the database: the restore,
// the drain (at most ten minutes) and the switch.
const pgSwitchWait = 30 * time.Minute

// TestManagedPostgresRollbackRestore is the restore-and-switch journey
// (#6146 PG-03, ADR-20261023181931). v2's release command deletes every
// row; `gregale rollback --restore-databases` undoes it by restoring the
// database to the moment that release command started and moving the app
// back to v1 on the restored copy. The original database keeps the write
// made after the bad release. The journey reports the time from the
// rollback to the first read on the copy and checks that no row written
// before the bad release is lost.
func TestManagedPostgresRollbackRestore(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	j := &postgresJourney{tg: tg, bindings: map[string]string{}}
	j.available(t)
	hidden := hiddenCapabilities(t, tg)
	skipHidden(t, hidden, "database-rollbacks")
	skipHidden(t, hidden, "release-commands")
	if j.databaseLimit < pgJourneysDatabases {
		t.Skipf("the PostgreSQL journeys hold up to %d databases at once; this account's limit is %d", pgJourneysDatabases, j.databaseLimit)
	}
	j.app = tg.app(t, "pg-rb")
	j.db = tg.slug("pg-rb")
	// The copy is purged first (cleanups run last-registered first): a
	// source cannot be deleted while a database restored from it exists.
	t.Cleanup(func() { j.purge(t, j.db) })
	t.Cleanup(func() {
		if j.restored != "" {
			j.purge(t, j.restored)
		}
	})
	var v1, v2 map[string]any
	s := newStory(t)
	s.step("create_database", func(t *testing.T) {
		tg.ok(t, time.Minute, "postgres", "create", j.db, "--region", j.region)
		j.waitReady(t, j.db)
	})
	// v1's release command needs the migration binding, and only an app
	// that exists can be bound: the probe is deployed without one first.
	s.step("deploy_app", func(t *testing.T) {
		receipt := tg.deploy(t, j.app, "--path", postgresProbeSource(t, ""))
		j.base = strings.TrimRight(str(receipt, "app_url"), "/")
		if j.base == "" {
			j.base = tg.appURL(j.app)
		}
	})
	s.step("attach_database", func(t *testing.T) {
		j.bindings[j.db] = j.bind(t, j.db, "DATABASE_URL")
		j.bind(t, j.db, "MIGRATION_DATABASE_URL", "--access", "migration")
	})
	s.step("release_v1", func(t *testing.T) {
		v1 = tg.deploy(t, j.app, "--path", postgresProbeSource(t, "create"), "--rollout", "immediate")
		j.firstAt = j.write(t, "DATABASE_URL", "first")
		j.expectRows(t, "DATABASE_URL", "first")
	})
	s.step("bad_release_v2", func(t *testing.T) {
		v2 = tg.deploy(t, j.app, "--path", postgresProbeSource(t, "wipe"), "--rollout", "immediate")
		j.expectRows(t, "DATABASE_URL")
		j.write(t, "DATABASE_URL", "after-wipe")
	})
	s.step("rollback_restores_database", func(t *testing.T) { j.rollbackRestore(t, str(v1, "id")) })
	s.check("restored_copy", j.restoredCopy)
	s.check("original_keeps_later_writes", func(t *testing.T) {
		j.attach(t, j.db, "ORIGINAL_DATABASE_URL")
		j.expectRows(t, "ORIGINAL_DATABASE_URL", "after-wipe")
		j.expectRows(t, "DATABASE_URL", "first")
	})
	s.check("undone_release_refused", func(t *testing.T) {
		res := tg.run(t, time.Minute, "rollback", j.app, "--to", str(v2, "id"), "--acknowledge-releases", "--no-wait")
		if code := res.problemCode(); res.exit == 0 || code != "rollback_target_database_restored" {
			t.Errorf("a rollback to the release the switch undid answered exit %d %s, want rollback_target_database_restored", res.exit, res.problem())
		}
	})
}

// rollbackRestore rolls the app back to v1 with --restore-databases and
// follows the switch to its end, then reads through the app: the copy holds
// the row written before the bad release and not the write after it.
func (j *postgresJourney) rollbackRestore(t *testing.T, v1 string) {
	start := time.Now()
	res := j.tg.ok(t, pgSwitchWait+5*time.Minute, "rollback", j.app, "--to", v1,
		"--restore-databases", "--acknowledge-releases", "--timeout", pgSwitchWait.String())
	sw := object(t, res.stdout)
	if str(sw, "phase") != "completed" || str(sw, "target_deployment_id") != v1 {
		t.Fatalf("the switch did not complete on v1: %s", compact(sw))
	}
	j.restored, j.restore = str(sw, "target_name"), str(sw, "restore_point")
	t.Logf("switch %s restored %s to %s as %s", str(sw, "id"), str(sw, "source_database_name"), j.restore, j.restored)
	if str(sw, "source_database_name") != j.db || j.restored == "" {
		t.Fatalf("the switch restored %q as %q, want %s", str(sw, "source_database_name"), j.restored, j.db)
	}
	j.expectRows(t, "DATABASE_URL", "first")
	t.Logf("metric postgres_rollback_restore_s=%.1f", time.Since(start).Seconds())
	// From the fence to the first read on the copy, by this machine's clock.
	if fenced, err := time.Parse(time.RFC3339Nano, str(sw, "fenced_at")); err == nil {
		t.Logf("metric postgres_rollback_unavailable_s=%.1f", time.Since(fenced).Seconds())
	}
}

// restoredCopy checks the database the app now runs on: restored to the
// switch's point, and an ordinary database rather than one holding the
// restore slot, whose bindings would be revoked when the slot ends
// (ADR-20261031123259).
func (j *postgresJourney) restoredCopy(t *testing.T) {
	doc := j.waitReady(t, j.restored)
	if str(doc, "restore_point_in_time") == "" {
		t.Errorf("the restored copy does not record its point in time: %s", compact(doc))
	}
	if until := str(doc, "restore_slot_until"); until != "" {
		t.Errorf("the app's database after the switch holds the restore slot until %s: %s", until, compact(doc))
	}
}
