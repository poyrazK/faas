// adr: 20261025110000
package main_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// `registry set --docker-config` copies the credential `docker login` stored
// for --registry; Docker Hub's API host matches Docker's index.docker.io key.
func TestRegistrySetFromDockerConfig(t *testing.T) {
	main.ResetJSONOut(t)
	body := `{"registry":"registry-1.docker.io","username":"hub","created_at":"2026-11-27T00:00:00Z","updated_at":"2026-11-27T00:00:00Z"}`
	f := main.AuthedFakeAPI(t, body, http.StatusOK)
	config := writeDockerConfig(t, `{"auths":{"https://index.docker.io/v1/":{"auth":"`+base64.StdEncoding.EncodeToString([]byte("hub:dckr_pat"))+`"}}}`)
	if code := main.CmdRegistrySet([]string{"--app", "demo", "--registry", "registry-1.docker.io", "--docker-config", config}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var got map[string]any
	if err := json.Unmarshal(*f.SawBody(), &got); err != nil {
		t.Fatal(err)
	}
	if got["registry"] != "https://registry-1.docker.io" || got["username"] != "hub" || got["password"] != "dckr_pat" {
		t.Fatalf("body = %v", got)
	}
}

func TestRegistrySetDockerConfigRefusals(t *testing.T) {
	config := writeDockerConfig(t, `{"auths":{"ghcr.io":{"auth":"`+base64.StdEncoding.EncodeToString([]byte("ci:tok"))+`"}}}`)
	for name, args := range map[string][]string{
		"with user":     {"--app", "demo", "--registry", "ghcr.io", "--docker-config", config, "--user", "u"},
		"no registry":   {"--app", "demo", "--docker-config", config},
		"no login":      {"--app", "demo", "--registry", "quay.io", "--docker-config", config},
		"with password": {"--app", "demo", "--registry", "ghcr.io", "--docker-config", config, "--password", "p"},
	} {
		t.Run(name, func(t *testing.T) {
			main.ResetJSONOut(t)
			f := main.AuthedFakeAPI(t, `{}`, http.StatusOK)
			if code := main.CmdRegistrySet(args); code == 0 {
				t.Fatal("registry set succeeded")
			}
			if *f.SawMethod() != "" {
				t.Fatalf("refused set still called %s %s", *f.SawMethod(), *f.SawPath())
			}
		})
	}
}

// `deploy --image --docker-config` saves the credential to the app under
// the host Gregale pulls from, then deploys; without a login it changes
// nothing.
func TestDeployImageWithDockerConfig(t *testing.T) {
	image := "ghcr.io/acme/api@sha256:" + strings.Repeat("a", 64)
	config := writeDockerConfig(t, `{"auths":{"ghcr.io":{"auth":"`+base64.StdEncoding.EncodeToString([]byte("ci:ghp_tok"))+`"}}}`)
	for _, tc := range []struct {
		name  string
		image string
		want  int
	}{
		{"saves then deploys", image, 0},
		{"no login for the registry", "quay.io/acme/api@sha256:" + strings.Repeat("a", 64), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := main.InitZeroConfigRepo(t)
			main.MustGit(t, repo, "remote", "remove", "origin")
			main.WithCwd(t, repo)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			var calls []string
			var cred api.PutAppRegistryCredentialRequest
			stub := main.NewZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, _ *main.ZeroConfigStubServer) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				switch {
				case r.URL.Path == "/v1/apps" && r.Method == http.MethodPost:
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "demo"})
				case r.URL.Path == "/v1/apps/demo/registry-credentials" && r.Method == http.MethodPut:
					_ = json.NewDecoder(r.Body).Decode(&cred)
					_ = json.NewEncoder(w).Encode(api.AppRegistryCredentialResponse{Registry: "ghcr.io", Username: "ci"})
				case r.URL.Path == "/v1/apps/demo/deployments" && r.Method == http.MethodPost:
					w.WriteHeader(http.StatusAccepted)
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "pending"})
				default:
					http.NotFound(w, r)
				}
			})
			t.Setenv("FAAS_API", (*stub.Srv()).URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			main.WithResetJSONOutput(t, false)
			code := main.CmdDeployTarball([]string{"--name", "demo", "--app", "--no-wait", "--image", tc.image, "--docker-config", config})
			if code != tc.want {
				t.Fatalf("code=%d want %d; calls=%v", code, tc.want, calls)
			}
			if tc.want != 0 {
				if len(calls) != 0 {
					t.Fatalf("a missing login still called the API: %v", calls)
				}
				return
			}
			if cred.Registry != "https://ghcr.io" || cred.Username != "ci" || cred.Password != "ghp_tok" {
				t.Fatalf("saved credential = %+v", cred)
			}
			put, post := dockerCallIndex(calls, "PUT /v1/apps/demo/registry-credentials"), dockerCallIndex(calls, "POST /v1/apps/demo/deployments")
			if put < 0 || post < 0 || put > post {
				t.Fatalf("credential must be saved before the deployment: %v", calls)
			}
		})
	}
}

func dockerCallIndex(calls []string, want string) int {
	for i, c := range calls {
		if c == want {
			return i
		}
	}
	return -1
}
