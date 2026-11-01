package journeys

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// GA inventory row: the Data API on managed PostgreSQL. Both are capabilities
// released per account, so the journey first checks that the account may use
// them; until it may, that step is a known failure (known_failures.json) and
// the rest report blocked.

// dataAPIRelease is the single-app migration: one table with row-level
// security, readable anonymously through the gregale_access claim, and one
// row named after the run. Release scripts may import only Node built-ins and
// the runtime's pg driver.
const dataAPIRelease = `import pg from 'pg'

const client = new pg.Client({ connectionString: process.env.MIGRATION_DATABASE_URL, connectionTimeoutMillis: 30000 })
await client.connect()
try {
  await client.query(` + "`" + `
    CREATE SCHEMA IF NOT EXISTS api;
    CREATE TABLE IF NOT EXISTS api.journey_notes (id text PRIMARY KEY, body text NOT NULL);
    ALTER TABLE api.journey_notes ENABLE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS anyone_reads ON api.journey_notes;
    CREATE POLICY anyone_reads ON api.journey_notes FOR SELECT
      USING ((current_setting('request.jwt.claims', true)::jsonb ->> 'gregale_access') IN ('anonymous', 'user'));
    CREATE TABLE IF NOT EXISTS api.journey_private (id text PRIMARY KEY);
    ALTER TABLE api.journey_private ENABLE ROW LEVEL SECURITY;` + "`" + `)
  await client.query('INSERT INTO api.journey_notes (id, body) VALUES ($1, $2) ON CONFLICT (id) DO UPDATE SET body = EXCLUDED.body',
    ['run', process.env.JOURNEY_NOTE ?? 'hello from the journey'])
} finally {
  await client.end()
}
`

// dataAPIJourney is the state TestDataAPI's steps share.
type dataAPIJourney struct {
	tg       *target
	database string
	app      string
	note     string
	deleted  bool
}

// TestDataAPI is a customer's Data API journey: create a managed database,
// create a single-app Data API whose release task applies a migration, read
// a public relation anonymously, check that other relations still need a
// token, check that the app cannot be deleted while its database credential
// is live, then tear the API down with data-api delete and delete the
// database.
func TestDataAPI(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	j := &dataAPIJourney{tg: tg, note: "hello from run " + tg.runID}
	j.app = tg.app(t, "data-api")
	j.database = tg.slug("data-db")
	// Deleting an app with a live database binding is refused, so the API
	// goes first, then the database (cleanups run last-registered first).
	t.Cleanup(func() { j.cleanupDatabase(t) })
	t.Cleanup(func() { j.cleanupAPI(t) })
	s := newStory(t)
	s.step("data_api_enabled", j.enabled)
	s.step("create_database", j.createDatabase)
	s.step("create_single_app_api", j.createAPI)
	s.check("health_reports_ready", j.health)
	s.check("anonymous_read_of_public_relation", j.anonymousRead)
	s.check("other_relations_need_a_token", j.tokenRequired)
	s.check("app_delete_refused_while_bound", j.appDeleteRefused)
	s.step("data_api_delete", j.deleteAPI)
}

// enabled checks that the account may use managed PostgreSQL and the Data API
// built on it.
func (j *dataAPIJourney) enabled(t *testing.T) {
	doc := object(t, j.tg.ok(t, time.Minute, "feature", "list").stdout)
	list, _ := field(doc, "capabilities").([]any)
	for _, want := range []string{"managed-postgres", "data-api"} {
		found := false
		for _, c := range list {
			if str(c, "key") != want {
				continue
			}
			found = true
			if str(c, "enabled") != "true" {
				t.Fatalf("%s is not enabled for this account (%s): %s", want, str(c, "unavailable_reason"), str(c, "unavailable_detail"))
			}
		}
		if !found {
			t.Fatalf("gregale feature list does not list %s", want)
		}
	}
}

func (j *dataAPIJourney) createDatabase(t *testing.T) {
	region := os.Getenv("GREGALE_E2E_POSTGRES_REGION")
	if region == "" {
		region = "us-east-2"
	}
	j.tg.ok(t, 5*time.Minute, "postgres", "create", j.database, "--region", region, "--class", "development", "--scale-to-zero")
	eventually(t, "database "+j.database+" ready", 15*time.Minute, 15*time.Second, func() (bool, string) {
		res := j.tg.run(t, time.Minute, "postgres", "get", j.database)
		if res.exit != 0 {
			return false, res.problem()
		}
		doc := object(t, res.stdout)
		return str(doc, "state") == "ready", "state " + str(doc, "state")
	})
}

func (j *dataAPIJourney) createAPI(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "migrations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release.mjs"), []byte(dataAPIRelease), 0o644); err != nil {
		t.Fatal(err)
	}
	// The API verifies tokens against a real issuer's keys; the journey sends
	// none, so any reachable JWKS will do.
	j.tg.ok(t, 25*time.Minute, "data-api", "create", j.app, "--database", j.database,
		"--issuer", "https://accounts.google.com", "--jwks-url", "https://www.googleapis.com/oauth2/v3/certs",
		"--audience", "gregale-e2e", "--public-read", "api.journey_notes", "--migrations", dir)
}

// health asks for the API's readiness and, where the control plane serves
// the runtime (ADR-20261031095954), that it runs the current one.
func (j *dataAPIJourney) health(t *testing.T) {
	var doc map[string]any
	eventually(t, "Data API "+j.app+" healthy", 5*time.Minute, 10*time.Second, func() (bool, string) {
		res := j.tg.run(t, time.Minute, "data-api", "health", j.app)
		doc = object(t, res.stdout)
		return res.exit == 0 && str(doc, "status") == "healthy", "status " + str(doc, "status") + " " + compact(field(doc, "current_findings"))
	})
	if state := str(doc, "runtime.state"); state != "" && state != "current" && state != "unknown" {
		t.Errorf("the API runs runtime %s, not the platform's current %s", str(doc, "runtime.running"), str(doc, "runtime.current"))
	}
}

func (j *dataAPIJourney) anonymousRead(t *testing.T) {
	url := j.tg.appURL(j.app) + "/rest/v1/journey_notes?select=id,body"
	var r httpResult
	eventually(t, "anonymous read of api.journey_notes", 3*time.Minute, 10*time.Second, func() (bool, string) {
		r = get(url)
		return r.status == http.StatusOK, r.String()
	})
	if !strings.Contains(string(r.body), `"id":"run"`) {
		t.Fatalf("the public relation does not return the migrated row: %s", r)
	}
}

func (j *dataAPIJourney) tokenRequired(t *testing.T) {
	base := j.tg.appURL(j.app) + "/rest/v1/"
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "journey_private"},
		{http.MethodPost, "journey_notes"},
	} {
		r := send(c.method, base+c.path, map[string]string{"Content-Type": "application/json"}, []byte(`{"id":"x","body":"x"}`))
		if r.status != http.StatusUnauthorized {
			t.Errorf("%s %s without a token = %s, want 401", c.method, c.path, r)
		}
	}
}

// appDeleteRefused checks that deleting the app alone is refused while its
// database credential is live, instead of leaving the credential behind.
func (j *dataAPIJourney) appDeleteRefused(t *testing.T) {
	res := j.tg.run(t, 2*time.Minute, "app", "delete", j.app, "--yes")
	if res.exit == 0 {
		j.deleted = true
		t.Fatal("app delete succeeded while the Data API's database binding was live")
	}
	if code := res.problemCode(); code != "app_has_postgres_bindings" {
		t.Fatalf("app delete refused with %q, want app_has_postgres_bindings: %s", code, res.problem())
	}
}

func (j *dataAPIJourney) deleteAPI(t *testing.T) {
	if j.deleted {
		t.Skip("the app was already deleted")
	}
	doc := object(t, j.tg.ok(t, 5*time.Minute, "data-api", "delete", j.app, "--yes").stdout)
	if str(doc, "deleted") != "true" {
		t.Fatalf("data-api delete: %s", compact(doc))
	}
	j.deleted = true
}

// cleanupAPI revokes the API's database bindings and deletes the app, so the
// app cleanup that runs next finds nothing left.
func (j *dataAPIJourney) cleanupAPI(t *testing.T) {
	if j.deleted || (j.tg.keep && t.Failed()) {
		return
	}
	if res := j.tg.run(t, 5*time.Minute, "data-api", "delete", j.app, "--yes"); res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
		t.Logf("warning: cleanup could not delete the Data API %s (%s); the next run's sweeper removes it", j.app, res.problem())
	}
}

func (j *dataAPIJourney) cleanupDatabase(t *testing.T) {
	if j.tg.keep && t.Failed() {
		return
	}
	if res := j.tg.run(t, 5*time.Minute, "postgres", "delete", j.database, "--purge", "--yes"); res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
		t.Logf("warning: cleanup could not delete the database %s (%s)", j.database, res.problem())
	}
}
