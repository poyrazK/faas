package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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

// adr: 20261016151947 — a backend's live_hub_url may carry {database_id} in
// its path; the hub answers its own database there and at /live, and nothing
// else.
func TestRealtimedMux(t *testing.T) {
	mux := realtimedMux("db_1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	for path, want := range map[string]int{
		"/live":           http.StatusTeapot,
		"/db_1/live":      http.StatusTeapot,
		"/db_2/live":      http.StatusNotFound,
		"/db_1/live/x":    http.StatusNotFound,
		"/":               http.StatusNotFound,
		"/db_1/db_1/live": http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
	}
}
