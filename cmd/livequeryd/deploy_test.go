package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/livequery/liveserver"
)

// adr: 20261016151947 — the connection string comes from a file in
// production, so its password stays out of the process list; one source only.
func TestDatabaseDSN(t *testing.T) {
	url, file := *databaseURL, *databaseURLFile
	defer func() { *databaseURL, *databaseURLFile = url, file }()
	path := filepath.Join(t.TempDir(), "dsn")
	if err := os.WriteFile(path, []byte("postgres://live@db/app\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, url, file, want string
		fails                 bool
	}{
		{name: "flag", url: "postgres://live@db/app", want: "postgres://live@db/app"},
		{name: "file", file: path, want: "postgres://live@db/app"},
		{name: "both", url: "postgres://live@db/app", file: path, fails: true},
		{name: "neither", fails: true},
		{name: "empty file", file: empty, fails: true},
		{name: "missing file", file: filepath.Join(t.TempDir(), "missing"), fails: true},
	} {
		*databaseURL, *databaseURLFile = c.url, c.file
		got, err := databaseDSN()
		if (err != nil) != c.fails || got != c.want {
			t.Errorf("%s: databaseDSN() = %q, %v", c.name, got, err)
		}
	}
}

// adr: 20261016151947 — a backend's live_hub_url carries {database_id} in its
// path. Each database the process serves answers there, 503 while its hub is
// starting; a database it does not serve is not found. /live answers only a
// process that serves one database.
func TestRealtimedMux(t *testing.T) {
	teapot := func(*liveserver.Hub) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	}
	hub := liveserver.New(liveserver.Config{Database: "db_1"})
	one := newRoutes([]string{"db_1"})
	one.set("db_1", hub)
	two := newRoutes([]string{"db_1", "db_2"})
	two.set("db_1", hub)
	for _, tc := range []struct {
		routes *routes
		paths  map[string]int
	}{
		{one, map[string]int{
			"/live": http.StatusTeapot, "/db_1/live": http.StatusTeapot, "/db_2/live": http.StatusNotFound,
			"/db_1/live/x": http.StatusNotFound, "/": http.StatusNotFound, "/db_1/db_1/live": http.StatusNotFound,
		}},
		{two, map[string]int{
			"/live": http.StatusNotFound, "/db_1/live": http.StatusTeapot, "/db_2/live": http.StatusServiceUnavailable,
			"/db_3/live": http.StatusNotFound,
		}},
	} {
		mux := realtimedMux(tc.routes, teapot)
		for path, want := range tc.paths {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != want {
				t.Errorf("serving %d databases: GET %s = %d, want %d", len(tc.routes.dbs), path, rec.Code, want)
			}
			if want == http.StatusServiceUnavailable && rec.Header().Get("Retry-After") == "" {
				t.Errorf("GET %s: 503 without Retry-After", path)
			}
		}
	}
}
