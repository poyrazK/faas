package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCmdMCPInitMaterializesEachLanguage(t *testing.T) {
	cases := []struct {
		language string
		files    []string
	}{
		{language: "node", files: []string{"package.json", "server.js", "gregale-mcp.json", "gregale.yaml"}},
		{language: "go", files: []string{"main.go", "auth.go", "tasks.go", "main_test.go", "go.mod", "go.sum", "gregale-mcp.json", "gregale.yaml"}},
		{language: "python", files: []string{"server.py", "auth.py", "tasks.py", "test_server.py", "requirements.txt", "gregale-mcp.json", "gregale.yaml"}},
	}
	for _, tc := range cases {
		t.Run(tc.language, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "mcp")
			stdout, _, restore := swapIO(t)
			defer restore()
			if code := cmdMCPInit([]string{"--path", dest, "--language", tc.language}); code != 0 {
				t.Fatalf("cmdMCPInit() = %d; output=%q", code, stdout.String())
			}
			for _, file := range tc.files {
				if _, err := os.Stat(filepath.Join(dest, file)); err != nil {
					t.Errorf("missing generated file %s: %v", file, err)
				}
			}
			if tc.language == "go" {
				if _, err := os.Stat(filepath.Join(dest, "main.go.tmpl")); !os.IsNotExist(err) {
					t.Errorf("Go source template was left beside generated source: %v", err)
				}
				module, err := os.ReadFile(filepath.Join(dest, "go.mod"))
				if _, err := os.Stat(filepath.Join(dest, "tasks.go.tmpl")); !os.IsNotExist(err) {
					t.Errorf("Go task source template was left beside generated source: %v", err)
				}
				if err != nil || !strings.Contains(string(module), "github.com/modelcontextprotocol/go-sdk v1.8.0") ||
					!strings.Contains(string(module), "github.com/jackc/pgx/v5 v5.11.0") ||
					!strings.Contains(string(module), "github.com/golang-jwt/jwt/v5 v5.3.1") {
					t.Errorf("generated Go module = %q, %v", module, err)
				}
			}
		})
	}
}

func TestCmdMCPInitRejectsUnknownLanguage(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "mcp")
	_, stderr, restore := swapIO(t)
	defer restore()
	if code := cmdMCPInit([]string{"--path", dest, "--language", "rust"}); code != 1 {
		t.Fatalf("cmdMCPInit() = %d, want 1", code)
	}
	if !strings.Contains(stderr(), "--language must be node, go, or python") {
		t.Fatalf("error did not explain supported languages: %q", stderr())
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("destination was created for invalid language: %v", err)
	}
}
