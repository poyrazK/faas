package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Prod test 2026-11-15 (deploy-path review P10): a plain `gregale deploy`
// never printed the missing required-secret warning. It ran only for callers
// that passed an already existing app (gregale dev, helper deploys), skipped
// first deploys, and read the declaration from the shell's directory instead
// of --path.
func TestDeployWarnsAboutMissingRequiredSecrets(t *testing.T) {
	for _, tc := range []struct {
		name        string
		secretsFile string
		wantWarning bool
	}{
		{name: "first deploy from --path without the secret", wantWarning: true},
		{name: "secrets file supplies the name", secretsFile: "E2E_REQUIRED_TOKEN=4f1c9a7e22b06d38\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := t.TempDir()
			for name, body := range map[string]string{
				"package.json":                   `{"name":"demo","scripts":{"start":"node index.js"}}` + "\n",
				"index.js":                       "require('http').createServer((q, s) => s.end('ok')).listen(process.env.PORT || 8080)\n",
				".gregale/required-secrets.json": `["E2E_REQUIRED_TOKEN"]` + "\n",
			} {
				path := filepath.Join(src, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			// The shell is elsewhere: the declaration must come from --path.
			main.WithCwd(t, t.TempDir())
			args := []string{"--path", src, "--name", "demo-req", "--no-wait"}
			if tc.secretsFile != "" {
				secretsPath := filepath.Join(t.TempDir(), "deploy.env")
				if err := os.WriteFile(secretsPath, []byte(tc.secretsFile), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--secrets-file", secretsPath)
			}

			var mu sync.Mutex
			var sealed []string
			stub := main.NewZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, _ *main.ZeroConfigStubServer) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case r.URL.Path == "/v1/apps" && r.Method == http.MethodPost:
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "demo-req"})
				case strings.HasPrefix(r.URL.Path, "/v1/apps/demo-req/secrets/") && r.Method == http.MethodPut:
					sealed = append(sealed, strings.TrimPrefix(r.URL.Path, "/v1/apps/demo-req/secrets/"))
					w.WriteHeader(http.StatusNoContent)
				case r.URL.Path == "/v1/apps/demo-req/secrets" && r.Method == http.MethodGet:
					var out api.AppSecretListResponse
					for _, key := range sealed {
						out.Secrets = append(out.Secrets, api.AppSecretResponse{Key: key, Scope: "default"})
					}
					_ = json.NewEncoder(w).Encode(out)
				case r.URL.Path == "/v1/apps/demo-req/env" && r.Method == http.MethodGet:
					_ = json.NewEncoder(w).Encode(api.AppEnvListResponse{})
				case r.URL.Path == "/v1/apps/demo-req/deployments" && r.Method == http.MethodPost:
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "a1"})
				default:
					http.Error(w, "no", http.StatusNotFound)
				}
			})
			t.Setenv("FAAS_API", (*stub.Srv()).URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			var stderr bytes.Buffer
			oldErr := *main.OsStderr
			*main.OsStderr = &stderr
			t.Cleanup(func() { *main.OsStderr = oldErr })

			if code := main.CmdDeployTarball(args); code != 0 {
				t.Fatalf("deploy exit = %d, want 0; stderr:\n%s", code, stderr.String())
			}
			warned := strings.Contains(stderr.String(), "E2E_REQUIRED_TOKEN")
			if warned != tc.wantWarning {
				t.Fatalf("warning about E2E_REQUIRED_TOKEN = %v, want %v; stderr:\n%s", warned, tc.wantWarning, stderr.String())
			}
		})
	}
}
