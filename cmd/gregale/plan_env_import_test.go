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

// The plan names the Compose values it will import and says why each other
// Compose value is not imported (ADR-20261023085230).
func TestProjectPlanShowsComposeEnvImport(t *testing.T) {
	for _, tc := range []struct {
		name     string
		workload api.PlanWorkload
		want     []string
		wantNot  []string
	}{
		{
			name: "opted in",
			workload: api.PlanWorkload{Name: "api", RootDir: "api",
				ImportedEnvKeys:    []string{"LOG_LEVEL", "NODE_ENV"},
				UnsetEnvKeys:       []string{"BILLING_URL", "STRIPE_KEY"},
				UnsetEnvKeyReasons: map[string]string{"BILLING_URL": "service_address", "STRIPE_KEY": "secret"}},
			want:    []string{"Imported from Compose", "LOG_LEVEL, NODE_ENV", "BILLING_URL (Docker service address)", "STRIPE_KEY (looks like a secret)"},
			wantNot: []string{"x-gregale-env-import"},
		},
		{
			name: "not opted in",
			workload: api.PlanWorkload{Name: "api", RootDir: "api",
				UnsetEnvKeys:       []string{"LOG_LEVEL"},
				UnsetEnvKeyReasons: map[string]string{"LOG_LEVEL": "not_opted_in"}},
			want:    []string{"LOG_LEVEL (plain value in Compose)", "x-gregale-env-import: true"},
			wantNot: []string{"Imported from Compose"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/projects/scan" {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "plan", ProjectSlug: "shop",
					Workloads: []api.PlanWorkload{tc.workload}})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_env_import")
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services:\n  api:\n    image: ghcr.io/example/api:v1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			old := *main.OsStdout
			*main.OsStdout = &output
			t.Cleanup(func() { *main.OsStdout = old })
			main.Run([]string{"scan", "--path", root})
			for _, want := range tc.want {
				if !strings.Contains(output.String(), want) {
					t.Errorf("output missing %q:\n%s", want, output.String())
				}
			}
			for _, unwanted := range tc.wantNot {
				if strings.Contains(output.String(), unwanted) {
					t.Errorf("output has %q:\n%s", unwanted, output.String())
				}
			}
		})
	}
}
