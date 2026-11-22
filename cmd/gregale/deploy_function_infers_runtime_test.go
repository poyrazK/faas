package main_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r27: `deploy --function` of a directory holding only handler.py
// answered "--function requires one of: node22, python312, …", although a
// plain `deploy` of the same directory infers python.
func TestDeployFunctionInfersTheRuntimeFromTheSource(t *testing.T) {
	for _, tc := range []struct {
		name, file  string
		wantRefusal bool
	}{
		{"python handler", "handler.py", false},
		{"no handler", "README.md", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte("def handler(event, context=None):\n    return {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("FAAS_TOKEN", "test")
			main.WithResetJSONOutput(t, false)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/account" {
					main.WriteJSONTest(w, api.AccountResponse{Plan: string(api.PlanPro)})
					return
				}
				api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "test stub"))
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			_, restoreOut := main.CaptureStdout(t)
			stderr, restoreErr := main.CaptureStderr(t)
			main.Run([]string{"deploy", "--function", "--name", "bh30-fn", "--dry-run", "--path", dir})
			restoreErr()
			restoreOut()
			refused := strings.Contains(stderr.String(), "--function requires one of")
			if refused != tc.wantRefusal {
				t.Fatalf("refused = %v, want %v; stderr:\n%s", refused, tc.wantRefusal, stderr.String())
			}
		})
	}
}
