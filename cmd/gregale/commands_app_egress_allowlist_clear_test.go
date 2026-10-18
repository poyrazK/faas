package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// H10-37: clear sent "egress_allowlist":null, which the API reads as
// "unchanged"; the CLI printed "cleared" and the list stayed.
func TestEgressAllowlistClearSendsEmptyList(t *testing.T) {
	var patched string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPatch {
			b, _ := io.ReadAll(r.Body)
			patched = string(b)
		}
		_, _ = io.WriteString(w, `{"slug":"demo","egress_allowlist":["1.1.1.1/32"]}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	if code := run([]string{"app", "demo", "egress-allowlist", "clear"}); code != 0 {
		t.Fatalf("clear exit = %d", code)
	}
	if !strings.Contains(patched, `"egress_allowlist":[]`) {
		t.Fatalf("PATCH body = %s, want an explicit empty egress_allowlist", patched)
	}
}
