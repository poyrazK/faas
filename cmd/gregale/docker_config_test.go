// adr: 20261025110000
package main_test

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func writeDockerConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDockerConfigRegistryAuth(t *testing.T) {
	basic := base64.StdEncoding.EncodeToString([]byte("ci:s3cr:et"))
	type call struct{ name, server string }
	for _, tc := range []struct {
		name     string
		config   string
		image    string
		helper   func(name, server string) (string, string, bool, error)
		want     *api.ImageRunRegistryAuth
		wantCall *call
		wantErr  string
	}{
		{name: "inline auth for the image's registry", image: "ghcr.io/acme/tool:1.4",
			config: `{"auths":{"https://ghcr.io":{"auth":"` + basic + `"},"quay.io":{"auth":"bm9wZTpub3Bl"}}}`,
			want:   &api.ImageRunRegistryAuth{Username: "ci", Password: "s3cr:et"}},
		{name: "username and password fields", image: "registry.example.com/team/app",
			config: `{"auths":{"registry.example.com":{"username":"bot","password":"token"}}}`,
			want:   &api.ImageRunRegistryAuth{Username: "bot", Password: "token"}},
		{name: "Docker Hub legacy key", image: "alpine",
			config: `{"auths":{"https://index.docker.io/v1/":{"auth":"` + basic + `"}}}`,
			want:   &api.ImageRunRegistryAuth{Username: "ci", Password: "s3cr:et"}},
		{name: "registry credential helper wins over the store", image: "ghcr.io/acme/tool:1.4",
			config: `{"credsStore":"desktop","credHelpers":{"ghcr.io":"gh"}}`,
			helper: func(string, string) (string, string, bool, error) { return "octo", "ghp_x", true, nil },
			want:   &api.ImageRunRegistryAuth{Username: "octo", Password: "ghp_x"}, wantCall: &call{"gh", "ghcr.io"}},
		{name: "credential store for Docker Hub", image: "acme/private",
			config: `{"credsStore":"desktop"}`,
			helper: func(string, string) (string, string, bool, error) { return "acme", "dckr_pat", true, nil },
			want:   &api.ImageRunRegistryAuth{Username: "acme", Password: "dckr_pat"}, wantCall: &call{"desktop", "https://index.docker.io/v1/"}},
		{name: "helper has nothing", image: "ghcr.io/acme/tool:1.4", config: `{"credsStore":"desktop"}`,
			helper:  func(string, string) (string, string, bool, error) { return "", "", false, nil },
			wantErr: "has no credentials for ghcr.io; run `docker login ghcr.io` or use --registry-username"},
		{name: "no entry", image: "ghcr.io/acme/tool:1.4", config: `{"auths":{"quay.io":{"auth":"` + basic + `"}}}`,
			wantErr: "has no credentials for ghcr.io"},
		{name: "identity token", image: "myacr.azurecr.io/app", config: `{"auths":{"myacr.azurecr.io":{"identitytoken":"eyJ"}}}`,
			wantErr: "identity token for myacr.azurecr.io"},
		{name: "helper identity token", image: "myacr.azurecr.io/app", config: `{"credsStore":"desktop"}`,
			helper:  func(string, string) (string, string, bool, error) { return "<token>", "eyJ", true, nil },
			wantErr: "identity token for myacr.azurecr.io"},
		{name: "helper name is not a word", image: "ghcr.io/acme/tool:1.4", config: `{"credsStore":"../evil"}`,
			wantErr: `credential helper "../evil" is not a valid name`},
		{name: "corrupt auth", image: "ghcr.io/acme/tool:1.4", config: `{"auths":{"ghcr.io":{"auth":"%%%"}}}`,
			wantErr: "ghcr.io auth is not base64"},
		{name: "not JSON", image: "alpine", config: `{`, wantErr: "parse docker config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got *call
			helper := func(_ context.Context, name, server string) (string, string, bool, error) {
				got = &call{name, server}
				if tc.helper == nil {
					t.Fatalf("unexpected helper call %s %s", name, server)
				}
				return tc.helper(name, server)
			}
			auth, err := main.DockerConfigRegistryAuth(t.Context(), writeDockerConfig(t, tc.config), tc.image, helper)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				if strings.Contains(err.Error(), "s3cr") || strings.Contains(err.Error(), "eyJ") {
					t.Fatalf("error leaks a secret: %v", err)
				}
				return
			}
			if err != nil || *auth != *tc.want {
				t.Fatalf("auth = %+v, %v; want %+v", auth, err, tc.want)
			}
			if (tc.wantCall == nil) != (got == nil) || (tc.wantCall != nil && *got != *tc.wantCall) {
				t.Fatalf("helper call = %+v, want %+v", got, tc.wantCall)
			}
		})
	}

	t.Run("a config.json path works too", func(t *testing.T) {
		dir := writeDockerConfig(t, `{"auths":{"ghcr.io":{"auth":"`+basic+`"}}}`)
		auth, err := main.DockerConfigRegistryAuth(t.Context(), filepath.Join(dir, "config.json"), "ghcr.io/acme/tool:1.4", nil)
		if err != nil || auth.Username != "ci" {
			t.Fatalf("auth = %+v, %v", auth, err)
		}
	})
	t.Run("missing config", func(t *testing.T) {
		if _, err := main.DockerConfigRegistryAuth(t.Context(), filepath.Join(t.TempDir(), "absent"), "alpine", nil); err == nil || !strings.Contains(err.Error(), "read docker config") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestParseImageRunArgsDockerConfig(t *testing.T) {
	opts, ok := main.ParseImageRunArgs([]string{"--docker-config", "~/.docker", "ghcr.io/acme/tool:1.4", "--version"})
	if !ok || opts.Request().Image != "ghcr.io/acme/tool:1.4" {
		t.Fatalf("parse = %+v, %v", opts.Request(), ok)
	}
	if _, ok := main.ParseImageRunArgs([]string{"--docker-config", "~/.docker", "--registry-username", "ci", "alpine"}); ok {
		t.Fatal("--docker-config with --registry-username was accepted")
	}
}
