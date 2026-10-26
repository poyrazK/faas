package main_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestCLIRoughEdgesH14_63 covers what the rc.294 re-test found after H14-42
// and H14-51: `env list` and `flags list` named no verb, `flags apply`
// without --file named nothing missing, and `jobs list --app` answered only
// "unknown flag --app".
func TestCLIRoughEdgesH14_63(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"env", "list"}, []string{`unknown subcommand "list"`, "Subcommands: create, clone-status, pull, push, diff, history, rollback"}},
		{[]string{"flags", "list", "--project", "shop"}, []string{`gregale flags: unknown subcommand "list"`, "Subcommands: get, apply"}},
		{[]string{"flags", "apply", "--project", "shop"}, []string{"gregale flags apply needs --file"}},
		{[]string{"jobs", "list", "--app", "shop"}, []string{"lists every job in the account; it has no --app filter"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			main.ResetJSONOut(t)
			_, stderr, restore := main.SwapIO(t)
			code := main.Run(tc.args)
			restore()
			if code != 1 {
				t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr())
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr(), want) {
					t.Errorf("stderr lacks %q:\n%s", want, stderr())
				}
			}
		})
	}
}

// TestDeploymentsTakesAPositionalSlug: `deployments h14-py` answered with bare
// usage while other app commands take the slug positionally (Hunt 14 H14-63).
func TestDeploymentsTakesAPositionalSlug(t *testing.T) {
	main.ResetJSONOut(t)
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"deployments":[]}`)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	_, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"deployments", "shop", "--limit", "2"})
	restore()
	if code != 0 || !strings.Contains(path, "/apps/shop/deployments") {
		t.Fatalf("exit=%d path=%q stderr=%q; want the app's deployment history", code, path, stderr())
	}
	_, stderr, restore = main.SwapIO(t)
	code = main.Run([]string{"deployments", "shop", "--app", "other"})
	restore()
	if code != 1 || !strings.Contains(stderr(), "usage: gregale deployments [SLUG | --app SLUG]") {
		t.Fatalf("conflicting slug and --app: exit=%d stderr=%q", code, stderr())
	}
}
