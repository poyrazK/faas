package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Prod hunt r38: `rollback` in a linked checkout printed only its usage.
func TestRollbackDefaultsToLinkedApp(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got == "" {
			got = r.URL.Path
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"title":"not found","status":404,"code":"not_found"}`))
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	linkCheckout(t)
	_, stdout, stderr := runCLI(t, "rollback", "--no-wait")
	if !strings.Contains(got, "/apps/demo") {
		t.Fatalf("first request %q, want the linked app demo\n%s%s", got, stdout, stderr)
	}
}

// Prod hunt r38: bare `gregale env` listed only the clone and sync verbs,
// not set/unset/list.
func TestEnvUsageListsSecretVerbs(t *testing.T) {
	_, _, stderr := runCLI(t, "env")
	if !strings.Contains(stderr, "set|unset|list") {
		t.Fatalf("env usage %q lacks set|unset|list", stderr)
	}
}
