package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestDomainDeleteTakesYesAndOtherwiseAsks pins Hunt 32 H32-14: a trailing
// --yes was rejected with the usage line, and without it the domain was
// deleted with no prompt.
func TestDomainDeleteTakesYesAndOtherwiseAsks(t *testing.T) {
	var deletes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/api.acme.com") {
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	_, _, restore := main.SwapIO(t)
	defer restore()

	for _, args := range [][]string{{"rm", "api.acme.com", "--yes"}, {"rm", "-y", "api.acme.com"}} {
		if code := main.CmdDomains(args); code != 0 {
			t.Fatalf("domain %q exit = %d", args, code)
		}
	}
	if got := deletes.Load(); got != 2 {
		t.Fatalf("deletes with --yes = %d, want 2", got)
	}
	// stdin is not a terminal in tests: without --yes the CLI refuses
	// instead of deleting.
	if code := main.CmdDomains([]string{"rm", "api.acme.com"}); code == 0 {
		t.Fatal("delete without --yes and no terminal succeeded")
	}
	if got := deletes.Load(); got != 2 {
		t.Fatalf("delete without --yes reached the API (%d deletes)", got)
	}
}
