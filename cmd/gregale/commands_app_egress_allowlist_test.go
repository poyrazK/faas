package main_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestEgressAllowlistEditsDoNotAskForARestart pins Hunt 32 H32-29: a first
// entry and a clear applied to the running instance within seconds, but the
// CLI said they waited for the next start and to run `app restart`.
func TestEgressAllowlistEditsDoNotAskForARestart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"slug":"demo","egress_allowlist":[]}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	out, restore := main.SwapStdout(t)
	defer restore()
	if code := main.Run([]string{"app", "demo", "egress-allowlist", "add", "1.1.1.1/32"}); code != 0 {
		t.Fatalf("add exit = %d", code)
	}
	if got := out.String(); strings.Contains(got, "restart") || !strings.Contains(got, "within seconds") {
		t.Fatalf("output = %q, want no restart advice", got)
	}
}
