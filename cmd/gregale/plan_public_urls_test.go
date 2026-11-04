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

// The project plan names every service it puts on the public internet, so a
// reviewer sees the exposure before applying, with --yes as well.
func TestProjectPlanListsPublicServices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/scan" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "plan", ProjectSlug: "shop",
			Workloads: []api.PlanWorkload{
				{Name: "web", RootDir: "web", PublicURL: "https://web.example.test"},
				{Name: "api", RootDir: "api", PublicURL: "https://shop-api.example.test"},
				{Name: "billing", RootDir: "billing", Visibility: api.AppVisibilityInternal},
			}})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_public_urls")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services:\n  web:\n    image: ghcr.io/example/web:v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &output
	t.Cleanup(func() { *main.OsStdout = old })

	for _, args := range [][]string{
		{"scan", "--path", root},
		{"deploy", "--project", "--path", root, "--source", "worktree", "--no-doctor", "--yes"},
	} {
		output.Reset()
		main.Run(args)
		start := strings.Index(output.String(), "Public on the internet:")
		if start < 0 {
			t.Fatalf("%v: no public section:\n%s", args, output.String())
		}
		section := output.String()[start:]
		if end := strings.Index(section[1:], "\n\n"); end >= 0 {
			section = section[:end+1]
		}
		if !strings.Contains(section, "https://web.example.test") || !strings.Contains(section, "https://shop-api.example.test") || strings.Contains(section, "billing") {
			t.Fatalf("%v: public section = %q", args, section)
		}
	}
}
