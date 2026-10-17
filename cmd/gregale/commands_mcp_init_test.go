package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestCmdMCPInitUsesAGregaleAuthApp(t *testing.T) {
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "1")
	resetJSONEnv(t)
	authIssuerServer(t, func(http.ResponseWriter, *http.Request) bool { return false })
	registration := false
	old := authAdminHTTPClient
	authAdminHTTPClient = &http.Client{Transport: authRoundTrip(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		writeJSONTest(recorder, map[string]any{"keys": map[string]any{"signing": "0A1B2C3D"}, "oauth": map[string]any{"enabled": r.URL.Host == "shop-auth.gregale.app", "dynamic_registration": registration, "issuer": "https://shop-auth.gregale.app/auth/v1"}})
		return recorder.Result(), nil
	})}
	t.Cleanup(func() { authAdminHTTPClient = old })
	dest := filepath.Join(t.TempDir(), "mcp")
	_, stderr, restore := swapIO(t)
	defer restore()
	if code := cmdMCPInit([]string{"--path", dest, "--auth-app", "shop-auth", "--resource", "https://shop-mcp.gregale.app/mcp"}); code != 0 {
		t.Fatalf("cmdMCPInit() = %d: %s", code, stderr())
	}
	raw, err := os.ReadFile(filepath.Join(dest, "gregale-mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Auth struct {
			Mode, Issuer, Resource string
			JWKSURL                string   `json:"jwks_url"`
			Scopes                 []string `json:"scopes"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.Mode != "external-oauth" || cfg.Auth.Issuer != "https://shop-auth.gregale.app/auth/v1" || cfg.Auth.JWKSURL != "https://shop-auth.gregale.app/auth/v1/.well-known/jwks.json" || strings.Join(cfg.Auth.Scopes, ",") != "openid" {
		t.Fatalf("auth = %+v", cfg.Auth)
	}
	if !strings.Contains(stderr(), "does not let clients register themselves") {
		t.Fatalf("no registration warning: %q", stderr())
	}
	for name, args := range map[string][]string{
		"issuer as well":     {"--issuer", "https://idp.example.com"},
		"open access":        {"--auth", "open"},
		"a scope Auth lacks": {"--scopes", "mcp:tools"},
	} {
		if code := cmdMCPInit(append([]string{"--path", filepath.Join(t.TempDir(), "x"), "--auth-app", "shop-auth", "--resource", "https://shop-mcp.gregale.app/mcp"}, args...)); code == 0 {
			t.Errorf("%s accepted", name)
		}
	}
}
