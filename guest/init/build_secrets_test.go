//go:build linux

// adr: 20261022185329
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildSecretsReachBuildctlByNameOnly(t *testing.T) {
	secrets := []api.DockerfileBuildSecret{{ID: "npm", Secret: "NPM_TOKEN"}, {ID: "gh.token", Secret: "GH_TOKEN"}}
	argv := buildArgv(api.BuildManifest{Framework: api.FrameworkDockerfile, Workdir: "/build/src", OutDir: "/build/out",
		DockerfileBuild: &api.DockerfileBuildOptions{Secrets: secrets}})
	var flags []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "--secret" {
			flags = append(flags, argv[i+1])
		}
	}
	if want := []string{"id=npm,env=GREGALE_BUILD_SECRET_0", "id=gh.token,env=GREGALE_BUILD_SECRET_1"}; !slices.Equal(flags, want) {
		t.Fatalf("--secret flags = %q, want %q", flags, want)
	}
	env, err := buildSecretEnvFrom(secrets, map[string]string{"NPM_TOKEN": "npm-value", "GH_TOKEN": "gh-value", "UNRELATED": "x"})
	if err != nil || !slices.Equal(env, []string{"GREGALE_BUILD_SECRET_0=npm-value", "GREGALE_BUILD_SECRET_1=gh-value"}) {
		t.Fatalf("env = %q, %v", env, err)
	}
	if _, err := buildSecretEnvFrom(secrets, map[string]string{"NPM_TOKEN": "npm-value"}); err == nil ||
		!strings.Contains(err.Error(), "gh.token (app secret GH_TOKEN) was not delivered") || strings.Contains(err.Error(), "npm-value") {
		t.Fatalf("undelivered secret error = %v", err)
	}
}

func TestBuildSecretEnvSkipsBuildsWithoutSecrets(t *testing.T) {
	dir := t.TempDir()
	for _, manifest := range []api.BuildManifest{
		{Framework: api.FrameworkDockerfile},
		{Framework: api.FrameworkDockerfile, DockerfileBuild: &api.DockerfileBuildOptions{Target: "runtime"}},
		{Framework: api.FrameworkRailpackNode, DockerfileBuild: &api.DockerfileBuildOptions{Secrets: []api.DockerfileBuildSecret{{ID: "npm", Secret: "NPM_TOKEN"}}}},
	} {
		if env, err := buildSecretEnvAt(manifest, filepath.Join(dir, "secrets.env"), filepath.Join(dir, "docker")); err != nil || env != nil {
			t.Fatalf("%+v: env=%q err=%v", manifest, env, err)
		}
	}
}

// ADR-20261026091500: vmmd delivers the app's registry logins as a Docker
// config in secrets.env; buildctl reads it through DOCKER_CONFIG, it is
// never a build secret, and secrets.env is gone afterwards.
func TestBuildSecretEnvStagesRegistryDockerConfig(t *testing.T) {
	dir := t.TempDir()
	secretsPath, dockerDir := filepath.Join(dir, "secrets.env"), filepath.Join(dir, "docker")
	config := `{"auths":{"ghcr.io":{"auth":"b2N0bzpwdw=="}}}`
	body, err := json.Marshal(map[string]string{api.BuildRegistryAuthKey: config, "NPM_TOKEN": "npm-value"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretsPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := api.BuildManifest{Framework: api.FrameworkDockerfile,
		DockerfileBuild: &api.DockerfileBuildOptions{Secrets: []api.DockerfileBuildSecret{{ID: "npm", Secret: "NPM_TOKEN"}}}}
	env, err := buildSecretEnvAt(manifest, secretsPath, dockerDir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"DOCKER_CONFIG=" + dockerDir, "GREGALE_BUILD_SECRET_0=npm-value"}; !slices.Equal(env, want) {
		t.Fatalf("env = %q, want %q", env, want)
	}
	got, err := os.ReadFile(filepath.Join(dockerDir, "config.json"))
	if err != nil || string(got) != config {
		t.Fatalf("config.json = %q, %v", got, err)
	}
	if info, err := os.Stat(filepath.Join(dockerDir, "config.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config.json mode = %v, %v", info, err)
	}
	if _, err := os.Stat(secretsPath); !os.IsNotExist(err) {
		t.Fatalf("secrets.env was not removed: %v", err)
	}
	// Declaring the reserved key as a build secret never mounts the config.
	if err := os.WriteFile(secretsPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	reserved := api.BuildManifest{Framework: api.FrameworkDockerfile,
		DockerfileBuild: &api.DockerfileBuildOptions{Secrets: []api.DockerfileBuildSecret{{ID: "cfg", Secret: api.BuildRegistryAuthKey}}}}
	if _, err := buildSecretEnvAt(reserved, secretsPath, dockerDir); err == nil {
		t.Fatal("the registry Docker config was mounted as a build secret")
	}
}
