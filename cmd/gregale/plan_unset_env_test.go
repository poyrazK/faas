package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// The project plan lists configuration a service names but will not have,
// and keys a --secrets-file supplies are not listed again.
func TestProjectPlanListsUnsetEnvKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/scan" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "plan", ProjectSlug: "shop",
			Workloads: []api.PlanWorkload{
				{Name: "api", RootDir: "api", UnsetEnvKeys: []string{"LOG_LEVEL", "STRIPE_KEY"}},
				{Name: "web", RootDir: "web"},
			}})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_unset_env")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services:\n  api:\n    image: ghcr.io/example/api:v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	secrets := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(secrets, []byte("STRIPE_KEY=sk_test_example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &output
	t.Cleanup(func() { *main.OsStdout = old })

	main.Run([]string{"scan", "--path", root})
	section := unsetSection(output.String())
	if !strings.Contains(section, "api") || !strings.Contains(section, "LOG_LEVEL, STRIPE_KEY") || strings.Contains(section, "web") {
		t.Fatalf("scan plan section = %q, output %s", section, output.String())
	}

	output.Reset()
	main.Run([]string{"deploy", "--project", "--path", root, "--source", "worktree", "--no-doctor", "--yes", "--secrets-file", secrets})
	section = unsetSection(output.String())
	if !strings.Contains(section, "LOG_LEVEL") || strings.Contains(section, "STRIPE_KEY") {
		t.Fatalf("deploy --yes section = %q, want LOG_LEVEL only; output %s", section, output.String())
	}
	if strings.Contains(output.String(), "sk_test_example") {
		t.Fatal("a secret value reached the output")
	}
}

func unsetSection(output string) string {
	start := strings.Index(output, "Needs a value")
	if start < 0 {
		return ""
	}
	return output[start:]
}
