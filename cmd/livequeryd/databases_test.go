package main

import (
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/livequery/liveserver"
	"github.com/onebox-faas/faas/pkg/wire"
)

// writeDatabasesFile writes body as a -databases-file beside two connection
// strings, a.dsn and b.dsn, under CREDENTIALS_DIRECTORY.
func writeDatabasesFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CREDENTIALS_DIRECTORY", dir)
	for name, dsn := range map[string]string{
		"a.dsn":   "postgres://live@db.example:5432/a\n",
		"b.dsn":   "postgres://live@db.example:5432/b",
		"neon":    "napi_test\n",
		"empty":   "\n",
		"notadsn": "host=%zz port=x\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(dsn), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "databases.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// adr: 20261016151947 — one process serves the databases a file lists: each
// with its own connection string (a systemd credential), slot, publication,
// checkpoint and scale-to-zero settings, and the single-database flags'
// defaults for what it leaves out.
func TestLoadDatabases(t *testing.T) {
	path := writeDatabasesFile(t, `{"databases": [
		{"id": "db_a", "database_url_file": "${CREDENTIALS_DIRECTORY}/a.dsn"},
		{"id": "db_b", "database_url_file": "${CREDENTIALS_DIRECTORY}/b.dsn", "slot": "live_b", "publication": "pub_b",
		 "install": false, "create_publication": false, "release_after": "5m", "awake": "neon", "awake_every": "2s",
		 "neon_project": "p-1", "neon_endpoint": "ep-1", "neon_api_key_file": "${CREDENTIALS_DIRECTORY}/neon"}
	]}`)
	dbs, err := loadDatabases(path, "/var/lib/faas/livequeryd")
	if err != nil {
		t.Fatal(err)
	}
	if len(dbs) != 2 {
		t.Fatalf("loaded %d databases", len(dbs))
	}
	a, b := dbs[0], dbs[1]
	if a.ID != "db_a" || a.DSN != "postgres://live@db.example:5432/a" || a.Slot != "gregale_live_db_a" || a.Publication != "gregale_live" ||
		!a.Install || !a.CreatePublication || a.ReleaseAfter != 0 || a.Awake != nil || a.Checkpoint != "/var/lib/faas/livequeryd/db_a.checkpoint" {
		t.Errorf("db_a = %+v", a)
	}
	if b.Slot != "live_b" || b.Publication != "pub_b" || b.Install || b.CreatePublication || b.ReleaseAfter != 5*time.Minute ||
		b.Awake == nil || b.AwakeEvery != 2*time.Second || b.Checkpoint != "/var/lib/faas/livequeryd/db_b.checkpoint" {
		t.Errorf("db_b = %+v", b)
	}
	if dbs, err := loadDatabases(path, ""); err != nil || dbs[0].Checkpoint != "" {
		t.Errorf("without -checkpoint-dir: checkpoint %q, %v", dbs[0].Checkpoint, err)
	}
}

// adr: 20261016151947 — a file that would serve a database wrongly is refused
// at start, before any database is opened.
func TestLoadDatabasesRefuses(t *testing.T) {
	a := `"database_url_file": "${CREDENTIALS_DIRECTORY}/a.dsn"`
	for name, body := range map[string]string{
		"no database":       `{"databases": []}`,
		"unknown field":     `{"databases": [{"id": "db_a", ` + a + `, "slots": "x"}]}`,
		"bad id":            `{"databases": [{"id": "db/a", ` + a + `}]}`,
		"id twice":          `{"databases": [{"id": "db_a", ` + a + `}, {"id": "db_a", ` + a + `, "slot": "other"}]}`,
		"one slot twice":    `{"databases": [{"id": "db_a", ` + a + `, "slot": "s"}, {"id": "db_b", ` + a + `, "slot": "s"}]}`,
		"slot across dbs":   `{"databases": [{"id": "db_a", ` + a + `, "slot": "s"}, {"id": "db_b", "database_url_file": "${CREDENTIALS_DIRECTORY}/b.dsn", "slot": "s"}]}`,
		"no url file":       `{"databases": [{"id": "db_a"}]}`,
		"missing url file":  `{"databases": [{"id": "db_a", "database_url_file": "${CREDENTIALS_DIRECTORY}/nope"}]}`,
		"empty url file":    `{"databases": [{"id": "db_a", "database_url_file": "${CREDENTIALS_DIRECTORY}/empty"}]}`,
		"not a dsn":         `{"databases": [{"id": "db_a", "database_url_file": "${CREDENTIALS_DIRECTORY}/notadsn"}]}`,
		"bad slot":          `{"databases": [{"id": "db_a", ` + a + `, "slot": "Live-Slot"}]}`,
		"release, no awake": `{"databases": [{"id": "db_a", ` + a + `, "release_after": "5m"}]}`,
		"neon, no endpoint": `{"databases": [{"id": "db_a", ` + a + `, "release_after": "5m", "awake": "neon", "neon_project": "p", "neon_api_key_file": "${CREDENTIALS_DIRECTORY}/neon"}]}`,
		"bad duration":      `{"databases": [{"id": "db_a", ` + a + `, "release_after": "soon", "awake": "neon"}]}`,
		"bad awake_every":   `{"databases": [{"id": "db_a", ` + a + `, "awake_every": "0s"}]}`,
	} {
		if _, err := loadDatabases(writeDatabasesFile(t, body), ""); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// One database, two entries: each decodes its own default slot.
	ok := `{"databases": [{"id": "db_a", ` + a + `}, {"id": "db_b", ` + a + `}]}`
	if _, err := loadDatabases(writeDatabasesFile(t, ok), ""); err != nil {
		t.Errorf("one database, two slots: %v", err)
	}
}

// adr: 20261016151947 — single-database flags beside -databases-file would be
// ignored, so they are refused.
func TestRefusePerDatabaseFlags(t *testing.T) {
	fs := flag.NewFlagSet("livequeryd", flag.ContinueOnError)
	fs.String("database-id", "", "")
	fs.String("listen", "", "")
	fs.Duration("release-after", 0, "")
	if err := fs.Parse([]string{"-listen=:9480"}); err != nil {
		t.Fatal(err)
	}
	if err := refusePerDatabaseFlags(fs); err != nil {
		t.Errorf("a shared flag was refused: %v", err)
	}
	if err := fs.Parse([]string{"-database-id=db_a", "-release-after=5m"}); err != nil {
		t.Fatal(err)
	}
	err := refusePerDatabaseFlags(fs)
	if err == nil || !strings.Contains(err.Error(), "-database-id") || !strings.Contains(err.Error(), "-release-after") {
		t.Errorf("per-database flags = %v", err)
	}
}

// adr: 20261016151947 — a database that keeps failing is retried at most once
// a minute, starting at one second.
func TestRestartBackoff(t *testing.T) {
	for failures, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 6: 32 * time.Second, 7: time.Minute, 50: time.Minute} {
		if got := restartBackoff(failures); got != want {
			t.Errorf("restartBackoff(%d) = %v, want %v", failures, got, want)
		}
	}
}

// adr: 20261016151947 — GET /databases tells operators and the deploy which
// databases have a running hub.
func TestDatabasesStatus(t *testing.T) {
	routes := newRoutes([]string{"db_b", "db_a"})
	routes.set("db_a", liveserver.New(liveserver.Config{Database: "db_a"}))
	routes.failed("db_b")
	routes.failed("db_b")
	mux := controlMux(wire.NewOpsMetrics("livequeryd_status_test"), &wire.ReadyzProbe{}, routes)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/databases", nil))
	var got struct {
		Databases []databaseStatus `json:"databases"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("%d %s: %v", rec.Code, rec.Body, err)
	}
	want := []databaseStatus{{ID: "db_a", Running: true}, {ID: "db_b", Restarts: 2}}
	if len(got.Databases) != 2 || got.Databases[0] != want[0] || got.Databases[1] != want[1] {
		t.Errorf("GET /databases = %+v, want %+v", got.Databases, want)
	}
}
