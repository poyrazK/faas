package journeys

import (
	"strings"
	"testing"
	"time"
)

// TestManagedPostgresRestoreSwitch is same-release recovery (#6146 PG-03,
// ADR-20261023181931 amendment 3): a bad write lands, and
// `gregale postgres restore --switch` restores the database to a moment
// before it and moves the app to the copy without changing its code. The
// copy holds every write up to that moment (the recovery point) and the
// journey reports the time from the command to the first read on the copy
// (the recovery time) and how long the app stopped serving.
func TestManagedPostgresRestoreSwitch(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	j := &postgresJourney{tg: tg, bindings: map[string]string{}}
	j.available(t)
	hidden := hiddenCapabilities(t, tg)
	skipHidden(t, hidden, "database-rollbacks")
	skipHidden(t, hidden, "release-commands")
	j.needRoomForAllJourneys(t)
	j.app = tg.app(t, "pg-rs")
	j.db = tg.slug("pg-rs")
	copyName := tg.slug("pg-rs-copy")
	// The copy is purged first (cleanups run last-registered first): a
	// source cannot be deleted while a database restored from it exists.
	t.Cleanup(func() { j.purge(t, j.db) })
	t.Cleanup(func() {
		if j.restored != "" {
			j.purge(t, j.restored)
		}
	})
	s := newStory(t)
	s.step("create_database", func(t *testing.T) {
		tg.ok(t, time.Minute, "postgres", "create", j.db, "--region", j.region)
		j.waitReady(t, j.db)
	})
	s.step("deploy_app", func(t *testing.T) {
		receipt := tg.deploy(t, j.app, "--path", postgresProbeSource(t, ""))
		j.base = strings.TrimRight(str(receipt, "app_url"), "/")
		if j.base == "" {
			j.base = tg.appURL(j.app)
		}
		j.bindings[j.db] = j.bind(t, j.db, "DATABASE_URL")
		j.bind(t, j.db, "MIGRATION_DATABASE_URL", "--access", "migration")
		tg.deploy(t, j.app, "--path", postgresProbeSource(t, "create"), "--rollout", "immediate")
	})
	var lastGood time.Time
	s.step("writes", func(t *testing.T) {
		j.write(t, "DATABASE_URL", "first")
		at, err := time.Parse(time.RFC3339Nano, j.write(t, "DATABASE_URL", "second"))
		if err != nil {
			t.Fatal(err)
		}
		lastGood = at
		// The bad write, far enough after the last good one to restore between.
		time.Sleep(time.Until(lastGood.Add(20 * time.Second)))
		j.write(t, "DATABASE_URL", "bad")
		j.expectRows(t, "DATABASE_URL", "first", "second", "bad")
	})
	s.step("restore_switch", func(t *testing.T) {
		if lastGood.IsZero() {
			t.Skip("no writes")
		}
		// One second after the last good commit: the copy must keep it.
		j.restore = lastGood.Add(time.Second).UTC().Format(time.RFC3339Nano)
		time.Sleep(time.Until(lastGood.Add(30 * time.Second)))
		start := time.Now()
		sw := object(t, tg.ok(t, pgSwitchWait+5*time.Minute, "postgres", "restore", j.db, "--name", copyName,
			"--point-in-time", j.restore, "--switch", j.app, "--yes", "--timeout", pgSwitchWait.String()).stdout)
		j.restored = str(sw, "target_name")
		if str(sw, "phase") != "completed" || j.restored != copyName {
			t.Fatalf("the switch did not complete onto %s: %s", copyName, compact(sw))
		}
		j.expectRows(t, "DATABASE_URL", "first", "second")
		t.Logf("metric postgres_restore_switch_s=%.1f", time.Since(start).Seconds())
		if fenced, err := time.Parse(time.RFC3339Nano, str(sw, "fenced_at")); err == nil {
			t.Logf("metric postgres_restore_switch_unavailable_s=%.1f", time.Since(fenced).Seconds())
		}
	})
	s.check("source_keeps_every_write", func(t *testing.T) {
		j.attach(t, j.db, "ORIGINAL_DATABASE_URL")
		j.expectRows(t, "ORIGINAL_DATABASE_URL", "first", "second", "bad")
	})
	s.check("restored_copy", j.restoredCopy)
}
