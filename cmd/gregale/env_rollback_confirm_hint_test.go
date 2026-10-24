package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Production-us 2026-10-22: a scripted `gregale env rollback 1 --app h12-go`
// was told to "pass --quiet to skip this confirmation", a flag env rollback
// rejects, and the prompt read "has 0 variables: " (Hunt 12 H12-56).
func TestEnvRollbackConfirmationNamesItsOwnBypass(t *testing.T) {
	rollbacks := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/config/revisions/1") {
			_ = json.NewEncoder(w).Encode(map[string]any{"revision": 1, "created_at": "2026-10-21T23:29:16Z", "variables": map[string]string{}})
			return
		}
		rollbacks++
		http.Error(w, "unexpected", http.StatusTeapot)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	t.Chdir(t.TempDir())
	main.ResetJSONOut(t)
	main.PipeStdin(t, "no\n")

	stderr, restore := main.CaptureStderr(t)
	code := main.CmdEnv([]string{"rollback", "1", "--app", "h12-go"})
	restore()
	got := stderr.String()
	if code == 0 || rollbacks != 0 {
		t.Fatalf("declined rollback: exit %d, rollback requests %d\n%s", code, rollbacks, got)
	}
	if !strings.Contains(got, "pass --yes to skip this confirmation") || strings.Contains(got, "--quiet") {
		t.Errorf("confirmation hint does not name --yes:\n%s", got)
	}
	if !strings.Contains(got, "has no variables") || strings.Contains(got, "variables: \n") {
		t.Errorf("empty revision rendered with a dangling list:\n%s", got)
	}
}
