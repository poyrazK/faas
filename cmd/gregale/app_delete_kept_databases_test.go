package main_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// prod 2026-11-19: deleting a Compose project's apps left the databases
// Compose had created without saying so.
func TestAppDeleteWithBindingsNamesTheKeptDatabases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/demo":
			_, _ = fmt.Fprint(w, `{"slug":"demo","id":"11111111-1111-1111-1111-111111111111"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/demo/postgres-bindings":
			_, _ = fmt.Fprint(w, `{"items":[{"id":"b1","database":"proj-db","state":"active"},{"id":"b2","database":"proj-db","state":"active"}]}`)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/postgres/bindings/"):
			_, _ = fmt.Fprint(w, `{}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/demo":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"title":"not found","status":404,"code":"not_found"}`)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	code, stdout, stderr := runCLI(t, "app", "delete", "demo", "--yes", "--delete-bindings")
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "kept, with their data") || strings.Count(stdout, "postgres delete proj-db") != 1 {
		t.Fatalf("stdout = %q", stdout)
	}
}
