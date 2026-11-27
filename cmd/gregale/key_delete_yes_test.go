package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// prod 2026-11-19: `key delete <id> --yes` printed only the usage line and
// left the key active.
func TestKeyDeleteAcceptsYes(t *testing.T) {
	var deleted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/keys/") {
			deleted = append(deleted, strings.TrimPrefix(r.URL.Path, "/v1/keys/"))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	const id = "4602a79e-7b6b-49e4-890a-e52dbe046437"
	if code, stdout, stderr := runCLI(t, "key", "delete", id, "--yes"); code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout, stderr)
	}
	if len(deleted) != 1 || deleted[0] != id {
		t.Fatalf("deleted = %v", deleted)
	}
}
