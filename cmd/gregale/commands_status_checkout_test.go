package main_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// ADR-20261026145832 step 3: in a linked checkout, status shows that app;
// the platform status is status --platform.
func TestStatusShowsTheLinkedApp(t *testing.T) {
	root := t.TempDir()
	if _, err := main.SaveProjectContext(root, main.LocalProjectContext{
		Version: main.ProjectContextVersion, Project: "shop", App: "api", Environment: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(root, ".gregale"))

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/api", "/v1/apps/other":
			_, _ = fmt.Fprintf(w, `{"id":"app-1","slug":%q,"status":"running","canonical_url":"https://api.example.com"}`, strings.TrimPrefix(r.URL.Path, "/v1/apps/"))
		case "/v1/account":
			_, _ = fmt.Fprint(w, `{"id":"acct-1","email":"dev@example.com","plan":"pro","status":"active"}`)
		case "/status/slo.json":
			_, _ = fmt.Fprint(w, `{"api_availability_pct":99.95,"build_success_pct":100,"as_of":"2026-10-26T10:00:00Z"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	code, stdout, stderr := runCLI(t, "status")
	for _, want := range []string{"shop (linked)", "api · staging", "running", "https://api.example.com", "dev@example.com (pro plan)", "gregale status --platform"} {
		if code != 0 || !strings.Contains(stdout, want) {
			t.Fatalf("status = %d, missing %q:\n%s%s", code, want, stdout, stderr)
		}
	}
	if !strings.Contains(stderr, "status --platform") {
		t.Errorf("status must say where the platform status went: %q", stderr)
	}

	paths = nil
	code, stdout, _ = runCLI(t, "status", "--platform")
	if code != 0 || !strings.Contains(stdout, "availability: 99.95%") || strings.Join(paths, " ") != "/status/slo.json" {
		t.Errorf("status --platform = %d, paths %v\n%s", code, paths, stdout)
	}

	code, stdout, _ = runCLI(t, "--json", "status", "--app", "other")
	var got struct {
		Project string `json:"project"`
		App     struct {
			Slug string `json:"slug"`
		} `json:"app"`
		Platform string `json:"platform_status_command"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); code != 0 || err != nil || got.App.Slug != "other" || got.Project != "shop" || got.Platform == "" {
		t.Errorf("--json status --app other = %d %v %+v\n%s", code, err, got, stdout)
	}
	if code, _, _ := runCLI(t, "status", "--platform", "--app", "api"); code == 0 {
		t.Error("--platform with --app must be a usage error")
	}
}

func TestStatusUnlinkedShowsThePlatform(t *testing.T) {
	t.Chdir(t.TempDir())
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = fmt.Fprint(w, `{"api_availability_pct":99.95,"build_success_pct":100,"as_of":"2026-10-26T10:00:00Z"}`)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	code, stdout, _ := runCLI(t, "status")
	if code != 0 || !strings.Contains(stdout, "availability:") || strings.Join(paths, " ") != "/status/slo.json" {
		t.Errorf("unlinked status = %d, paths %v\n%s", code, paths, stdout)
	}
}
