package journeys

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// GA inventory row 109: managed PostgreSQL (ADR-20261031091607). The
// journey's app (testdata/postgres-probe) writes and reads rows through the
// connection URL a binding delivers, so every step checks the data a
// customer would see, not only the API's state.

// pgDatabaseWait bounds a database's way to ready: provisioning, a restore,
// or a recover's release of the provider hold.
const pgDatabaseWait = 10 * time.Minute

// pgIdleSuspend is the provider's idle suspend timeout for a scale-to-zero
// database (docs/managed-postgres.md: 300 seconds).
const pgIdleSuspend = 5 * time.Minute

// postgresJourney is the state TestManagedPostgres's steps share.
type postgresJourney struct {
	tg               *target
	app, base        string
	region           string
	classes          []string // service classes the plan offers
	databaseLimit    int
	db, restored     string // database names
	bindings         map[string]string
	firstAt, restore string // the first row's commit time and the restore point
}

// TestManagedPostgres is a customer's managed PostgreSQL journey (row 109):
// create a database and attach it to an app, write and read rows through
// the app, restore a point between two writes into a new database and read
// that, delete the database (an undoable delete), recover it with its rows,
// and purge it.
func TestManagedPostgres(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	j := &postgresJourney{tg: tg, bindings: map[string]string{}}
	j.app = tg.app(t, "pg")
	j.db, j.restored = tg.slug("pg"), tg.slug("pg-restored")
	// Bindings go before databases and databases before the app (cleanups
	// run last-registered first): an attached database cannot be deleted.
	t.Cleanup(func() { j.purge(t, j.db) })
	t.Cleanup(func() { j.purge(t, j.restored) })
	s := newStory(t)
	s.step("postgres_available", j.available)
	s.step("create_database", func(t *testing.T) {
		tg.ok(t, time.Minute, "postgres", "create", j.db, "--region", j.region)
		j.waitReady(t, j.db)
	})
	s.step("deploy_app", func(t *testing.T) {
		receipt := tg.deploy(t, j.app, "--path", postgresProbeSource(t))
		j.base = strings.TrimRight(str(receipt, "app_url"), "/")
		if j.base == "" {
			j.base = tg.appURL(j.app)
		}
	})
	s.step("attach_database", func(t *testing.T) { j.attach(t, j.db, "DATABASE_URL") })
	s.check("binding_verify", func(t *testing.T) {
		tg.ok(t, 2*time.Minute, "app", "binding", "verify", j.app, "--postgres", "DATABASE_URL")
	})
	s.step("write_and_read", func(t *testing.T) {
		j.firstAt = j.write(t, "DATABASE_URL", "first")
		j.expectRows(t, "DATABASE_URL", "first")
	})
	s.check("credential_rotation", j.rotateCredential)
	s.check("resize", j.resize)
	s.check("idle_wake", j.idleWake)
	s.step("restore_point_in_time", j.restoreBetweenWrites)
	s.step("purge_restored", func(t *testing.T) {
		if j.restore == "" {
			t.Skip("no restore ran")
		}
		j.detach(t, j.restored)
		j.purgeNow(t, j.restored)
	})
	s.step("scheduled_delete", j.scheduledDelete)
	s.step("recover", j.recover)
	s.step("purge", func(t *testing.T) {
		j.detach(t, j.db)
		j.purgeNow(t, j.db)
	})
}

// available reads the region the account may create in and its database
// limit; with provisioning closed the journey cannot run.
func (j *postgresJourney) available(t *testing.T) {
	doc := object(t, j.tg.ok(t, time.Minute, "postgres", "capabilities").stdout)
	if str(doc, "provisioning_enabled") != "true" {
		t.Fatalf("managed PostgreSQL provisioning is closed for this account: %s", compact(doc))
	}
	j.region, j.databaseLimit = str(doc, "region"), int(num(doc, "database_limit"))
	classes, _ := field(doc, "service_classes").([]any)
	for _, class := range classes {
		j.classes = append(j.classes, fmt.Sprint(class))
	}
	if j.region == "" || j.databaseLimit < 1 {
		t.Fatalf("no region or database allowance: %s", compact(doc))
	}
	t.Logf("region %s, database limit %d", j.region, j.databaseLimit)
}

func (j *postgresJourney) waitReady(t *testing.T, name string) map[string]any {
	t.Helper()
	var last map[string]any
	eventually(t, name+" ready", pgDatabaseWait, 5*time.Second, func() (bool, string) {
		last = object(t, j.tg.ok(t, time.Minute, "postgres", "get", name).stdout)
		switch str(last, "state") {
		case "failed", "deleted":
			t.Fatalf("%s is %s: %s", name, str(last, "state"), compact(last))
		}
		return str(last, "state") == "ready", compact(last)
	})
	return last
}

// attach binds name to the app as key, waits for the binding, and restarts
// the app so its instances start with the new secret.
func (j *postgresJourney) attach(t *testing.T, name, key string) {
	t.Helper()
	binding := object(t, j.tg.ok(t, 2*time.Minute, "postgres", "attach", name, j.app, "--env", key).stdout)
	j.bindings[name] = str(binding, "id")
	eventually(t, key+" binding ready", 5*time.Minute, 5*time.Second, func() (bool, string) {
		list := object(t, j.tg.ok(t, time.Minute, "postgres", "bindings", "list", name).stdout)
		items, _ := field(list, "items").([]any)
		for _, item := range items {
			if str(item, "id") == j.bindings[name] {
				return str(item, "state") == "ready", compact(item)
			}
		}
		return false, compact(list)
	})
	j.tg.ok(t, 12*time.Minute, "app", j.app, "restart", "--fresh", "--wait", "--timeout", "10m")
}

// detach deletes the app's binding to name and waits until it is gone: a
// database with a binding cannot be deleted.
func (j *postgresJourney) detach(t *testing.T, name string) {
	t.Helper()
	id := j.bindings[name]
	if id == "" {
		return
	}
	j.tg.ok(t, time.Minute, "postgres", "bindings", "delete", id)
	eventually(t, name+" binding deleted", 5*time.Minute, 5*time.Second, func() (bool, string) {
		list := object(t, j.tg.ok(t, time.Minute, "postgres", "bindings", "list", name).stdout)
		items, _ := field(list, "items").([]any)
		for _, item := range items {
			if str(item, "id") == id && str(item, "state") != "deleted" {
				return false, compact(item)
			}
		}
		return true, ""
	})
	delete(j.bindings, name)
}

// notes calls the app's /notes for the database bound as key. The first
// call after an attach may wake the app and the database's compute, so a
// 5xx is retried until timeout.
func (j *postgresJourney) notes(t *testing.T, method, key, body string) map[string]any {
	t.Helper()
	target := j.base + "/notes?env=" + url.QueryEscape(key)
	if body != "" {
		target += "&body=" + url.QueryEscape(body)
	}
	var last httpResult
	eventually(t, method+" /notes "+key, 3*time.Minute, 5*time.Second, func() (bool, string) {
		last = send(method, target, nil, nil)
		return last.err == nil && last.status == http.StatusOK, last.String()
	})
	return jsonBody(t, last)
}

// write inserts body and returns the row's commit time (database clock).
func (j *postgresJourney) write(t *testing.T, key, body string) string {
	t.Helper()
	doc := j.notes(t, http.MethodPost, key, body)
	if str(doc, "body") != body || str(doc, "at") == "" {
		t.Fatalf("write %q answered %s", body, compact(doc))
	}
	return str(doc, "at")
}

func (j *postgresJourney) expectRows(t *testing.T, key string, want ...string) {
	t.Helper()
	doc := j.notes(t, http.MethodGet, key, "")
	list, _ := field(doc, "bodies").([]any)
	got := make([]string, 0, len(list))
	for _, v := range list {
		got = append(got, fmt.Sprint(v))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s holds rows %v, want %v", key, got, want)
	}
}

// rotateCredential rotates the app's binding and restarts the app on the new
// credential; the rows must still be there.
func (j *postgresJourney) rotateCredential(t *testing.T) {
	id := j.bindings[j.db]
	before := object(t, j.tg.ok(t, time.Minute, "postgres", "bindings", "get", id).stdout)
	rotated := object(t, j.tg.ok(t, 7*time.Minute, "postgres", "bindings", "rotate", id, "--wait").stdout)
	if num(rotated, "credential_generation") <= num(before, "credential_generation") || str(rotated, "rotation_pending") == "true" {
		t.Fatalf("rotation did not finish with a new credential: before %s, after %s", compact(before), compact(rotated))
	}
	j.tg.ok(t, 12*time.Minute, "app", j.app, "restart", "--fresh", "--wait", "--timeout", "10m")
	j.expectRows(t, "DATABASE_URL", "first")
}

// resize moves the database to burstable when the plan offers it (Pro and
// Scale); the rows must survive the compute change.
func (j *postgresJourney) resize(t *testing.T) {
	if !slices.Contains(j.classes, "burstable") {
		t.Skipf("the plan offers %v, no class to resize to", j.classes)
	}
	h := randomHex(t, 32)
	request := h[0:8] + "-" + h[8:12] + "-4" + h[13:16] + "-a" + h[17:20] + "-" + h[20:32]
	j.tg.ok(t, 2*time.Minute, "postgres", "resize", j.db, "--class", "burstable", "--request-id", request)
	eventually(t, "resize succeeded", 10*time.Minute, 10*time.Second, func() (bool, string) {
		doc := object(t, j.tg.ok(t, time.Minute, "postgres", "resize-status", j.db, request).stdout)
		if code := str(doc, "last_error_code"); code != "" {
			t.Fatalf("resize failed with %s: %s", code, compact(doc))
		}
		return str(doc, "state") == "succeeded", compact(doc)
	})
	if doc := object(t, j.tg.ok(t, time.Minute, "postgres", "get", j.db).stdout); str(doc, "service_class") != "burstable" {
		t.Errorf("resized database reads as %s", compact(doc))
	}
	j.expectRows(t, "DATABASE_URL", "first")
}

// idleWake leaves the app and the database idle past the database's suspend
// timeout, then times the first read: both wake for it (#6146 PG-07, the
// application-plus-database wake latency).
func (j *postgresJourney) idleWake(t *testing.T) {
	time.Sleep(pgIdleSuspend + time.Minute)
	doc := object(t, j.tg.ok(t, time.Minute, "postgres", "get", j.db).stdout)
	t.Logf("compute state after %s idle: %q", pgIdleSuspend+time.Minute, str(doc, "health.compute_state"))
	start := time.Now()
	j.expectRows(t, "DATABASE_URL", "first")
	t.Logf("metric postgres_idle_wake_read_s=%.1f", time.Since(start).Seconds())
}

// restoreBetweenWrites writes a second row 20 s after the first, restores
// the point halfway between them into a new database, and reads it through
// a second binding: it must hold the first row only. On a one-database plan
// the copy goes past the limit and holds the restore slot
// (ADR-20261031123259).
func (j *postgresJourney) restoreBetweenWrites(t *testing.T) {
	first, err := time.Parse(time.RFC3339Nano, j.firstAt)
	if err != nil {
		t.Fatalf("first row time %q: %v", j.firstAt, err)
	}
	time.Sleep(time.Until(first.Add(20 * time.Second)))
	second, err := time.Parse(time.RFC3339Nano, j.write(t, "DATABASE_URL", "second"))
	if err != nil {
		t.Fatal(err)
	}
	j.restore = first.Add(second.Sub(first) / 2).UTC().Format(time.RFC3339Nano)
	// The point must be in the past for the provider as well.
	time.Sleep(time.Until(second.Add(10 * time.Second)))
	j.tg.ok(t, time.Minute, "postgres", "restore", j.db, "--name", j.restored, "--point-in-time", j.restore)
	doc := j.waitReady(t, j.restored)
	if str(doc, "restore_point_in_time") == "" {
		t.Errorf("restored database does not record its point in time: %s", compact(doc))
	}
	if j.databaseLimit == 1 && str(doc, "restore_slot_until") == "" {
		t.Errorf("a restore past a one-database limit does not hold the restore slot: %s", compact(doc))
	}
	j.attach(t, j.restored, "RESTORED_DATABASE_URL")
	j.expectRows(t, "RESTORED_DATABASE_URL", "first")
	j.expectRows(t, "DATABASE_URL", "first", "second")
}

// scheduledDelete deletes the database without --purge: it must stay listed
// with an undo window and refuse a second database of the same name.
func (j *postgresJourney) scheduledDelete(t *testing.T) {
	j.detach(t, j.db)
	doc := object(t, j.tg.ok(t, time.Minute, "postgres", "delete", j.db, "--yes").stdout)
	until, err := time.Parse(time.RFC3339Nano, str(doc, "restorable_until"))
	if str(doc, "state") != "deleting" || err != nil {
		t.Fatalf("delete did not schedule: %s", compact(doc))
	}
	if left := time.Until(until); left < 6*24*time.Hour || left > 8*24*time.Hour {
		t.Errorf("restorable_until is %s away, want about 7 days", left.Round(time.Hour))
	}
	if res := j.tg.run(t, time.Minute, "postgres", "create", j.db, "--region", j.region); res.exit == 0 {
		t.Errorf("a second database took the name of one scheduled for deletion")
	}
}

// recover undoes the delete; the rows written before it must be back.
func (j *postgresJourney) recover(t *testing.T) {
	j.tg.ok(t, 5*time.Minute, "postgres", "recover", j.db)
	doc := j.waitReady(t, j.db)
	if str(doc, "restorable_until") != "" {
		t.Errorf("a recovered database still shows an undo window: %s", compact(doc))
	}
	j.attach(t, j.db, "DATABASE_URL")
	want := []string{"first", "second"}
	if j.restore == "" {
		want = want[:1]
	}
	j.expectRows(t, "DATABASE_URL", want...)
}

// purgeNow deletes name with --purge and waits until it is gone: the source
// of a restore cannot be deleted while the restored database exists.
func (j *postgresJourney) purgeNow(t *testing.T, name string) {
	t.Helper()
	doc := object(t, j.tg.ok(t, time.Minute, "postgres", "delete", name, "--purge", "--yes").stdout)
	if str(doc, "restorable_until") != "" {
		t.Fatalf("a purge left an undo window: %s", compact(doc))
	}
	eventually(t, name+" purged", pgDatabaseWait, 5*time.Second, func() (bool, string) {
		res := j.tg.run(t, time.Minute, "postgres", "get", name)
		if res.exit != 0 {
			return strings.Contains(res.problem(), "not_found"), res.problem()
		}
		doc := object(t, res.stdout)
		return str(doc, "state") == "deleted", compact(doc)
	})
}

// purge removes name for good, whatever state the journey left it in. It
// is the cleanup, so it only logs failures.
func (j *postgresJourney) purge(t *testing.T, name string) {
	if res := j.tg.run(t, time.Minute, "postgres", "get", name); res.exit != 0 {
		return
	} else if state := str(object(t, res.stdout), "state"); state == "deleted" {
		return
	}
	if id := j.bindings[name]; id != "" {
		j.tg.run(t, time.Minute, "postgres", "bindings", "delete", id)
		time.Sleep(30 * time.Second)
	}
	if res := j.tg.run(t, time.Minute, "postgres", "delete", name, "--purge", "--yes"); res.exit != 0 {
		t.Logf("warning: could not purge %s (%s); delete it by hand: gregale postgres delete %s --purge --yes", name, res.problem(), name)
	}
}

// postgresProbeSource copies testdata/postgres-probe into a fresh
// directory, so its source digest is fixed and later runs hit the build
// cache.
func postgresProbeSource(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join("testdata", "postgres-probe")
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read postgres probe source: %v", err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read postgres probe source: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			t.Fatalf("write postgres probe source: %v", err)
		}
	}
	return dir
}
