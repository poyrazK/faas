package main_test

import (
	"bytes"
	"github.com/onebox-faas/faas/cmd/gregale"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestPostgresCommandsResolveDatabaseNames pins H9-P108: `postgres get
// orders` answered "invalid managed postgres request" because get, delete,
// restore and bindings list sent the name where the API expects an ID,
// while attach and resize resolved the same name.
func TestPostgresCommandsResolveDatabaseNames(t *testing.T) {
	const id = "5b0c6f0e-6a55-4d3b-9b8e-2f1a0c9d7e61"
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases":
			_, _ = w.Write([]byte(`{"items":[{"id":"` + id + `","name":"orders","state":"ready"}]}`))
		case r.URL.Path == "/v1/postgres/databases/"+id:
			_, _ = w.Write([]byte(`{"id":"` + id + `","name":"orders","state":"ready"}`))
		default:
			http.Error(w, `{"status":404,"code":"not_found"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	previous := (*main.OsStdout)
	(*main.OsStdout) = &bytes.Buffer{}
	t.Cleanup(func() { (*main.OsStdout) = previous })

	deleteWithoutPrompt := func(args []string) int { return main.CmdPostgresDelete(append(args, "--yes")) }
	for name, run := range map[string]func([]string) int{"get": main.CmdPostgresGet, "delete": deleteWithoutPrompt} {
		paths = nil
		if code := run([]string{"orders"}); code != 0 {
			t.Fatalf("postgres %s orders = %d; requests %v", name, code, paths)
		}
		want := map[string]string{"get": "GET", "delete": "DELETE"}[name] + " /v1/postgres/databases/" + id
		if len(paths) != 2 || paths[1] != want {
			t.Fatalf("postgres %s requests = %v, want list then %s", name, paths, want)
		}
	}
	paths = nil
	if code := main.CmdPostgresGet([]string{id}); code != 0 || len(paths) != 1 {
		t.Fatalf("get by ID = %d, requests %v; an ID must skip the catalog lookup", code, paths)
	}
}

// TestPostgresGetMissingNameIsNotFound: a name no database carries is
// not_found (exit 4), not invalid_request (exit 1). Production 2026-11-19:
// TestManagedPostgres/purge_restored waited ten minutes for a purged database
// to report not_found while the CLI kept answering invalid_request.
func TestPostgresGetMissingNameIsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/postgres/databases" {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.Error(w, `{"status":500}`, http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	if code := main.CmdPostgresGet([]string{"purged"}); code != 4 {
		t.Fatalf("postgres get purged = %d, want 4 (not_found)", code)
	}
}
