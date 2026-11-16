package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func openAPISpec(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "pkg", "mcpopenapi", "testdata", "reports.json"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "api.json")
	if err := os.WriteFile(path, source, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMCPImportOpenAPIMaterializesReviewedProject(t *testing.T) {
	resetJSONEnv(t)
	output, stderr, restore := main.SwapIO(t)
	defer restore()
	dir := filepath.Join(t.TempDir(), "reports")
	if code := main.Run([]string{"mcp", "server", "import", "--spec", openAPISpec(t), "--path", dir,
		"--server-url", "https://api.example.com/v1", "--operations", "getReport,createReport", "--json"}); code != 0 {
		t.Fatalf("import=%d: %s", code, stderr())
	}
	var result struct {
		Runtime, Status       string
		FixturesRequireReview bool `json:"fixtures_require_review"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || !result.FixturesRequireReview || result.Status != "created" || result.Runtime != "node" {
		t.Fatal("generator output", result, err)
	}
	for _, name := range []string{"package.json", "package-lock.json", "app.js", "server.js", "openapi-tools.js",
		"openapi-http.js", "openapi-tools.json", "README.md", "AGENTS.md", "test/openapi-generated.test.js"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing project file %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "test", "http.test.js")); !os.IsNotExist(err) {
		t.Fatal("example-catalog tests were retained in the generated application")
	}
	config, err := mcphosting.Load(dir)
	if err != nil || config.Auth.Mode != "open" || len(config.Auth.ToolScopes) != 2 {
		t.Fatal("generated hosting configuration", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "gregale-mcp.tests.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mcphosting.LoadSmokeSuite(bytes.NewReader(body)); err == nil {
		t.Fatal("unreviewed fixture examples passed behavioral validation")
	}
	if _, err := os.Stat(filepath.Join(dir, "gregale-mcp.tests.json")); !os.IsNotExist(err) {
		t.Fatal("generated project had apparent behavioral acceptance evidence")
	}
}

func TestMCPImportOpenAPIRefusesBeforePublishingFiles(t *testing.T) {
	resetJSONEnv(t)
	_, stderr, restore := main.SwapIO(t)
	defer restore()
	source := openAPISpec(t)
	for name, extra := range map[string][]string{
		"private target":    {"--server-url", "https://127.0.0.1"},
		"missing operation": {"--operations", "absent"},
		"private operation": {"--operations", "privateReport"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "project")
			args := []string{"--spec", source, "--path", dir, "--server-url", "https://api.example.com", "--operations", "getReport"}
			if code := main.CmdMCPImportOpenAPI(append(args, extra...)); code == 0 {
				t.Fatal("invalid import succeeded")
			}
			if _, err := os.Lstat(dir); !os.IsNotExist(err) {
				t.Fatalf("failed generation published a destination: %v; %s", err, stderr())
			}
		})
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "keep")
	if err := os.WriteFile(marker, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := main.CmdMCPImportOpenAPI([]string{"--spec", source, "--path", dir, "--server-url", "https://api.example.com", "--operations", "getReport"}); code == 0 {
		t.Fatal("import overwrote a nonempty destination")
	}
	data, _ := os.ReadFile(marker)
	if string(data) != "original" || !strings.Contains(stderr(), "directory is not empty") {
		t.Fatal("existing destination was not preserved")
	}
	link := filepath.Join(t.TempDir(), "linked-spec.json")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if code := main.CmdMCPImportOpenAPI([]string{"--spec", link, "--path", filepath.Join(t.TempDir(), "linked-output"), "--server-url", "https://api.example.com", "--operations", "getReport"}); code == 0 {
		t.Fatal("import followed a customer-controlled source symlink")
	}
}
