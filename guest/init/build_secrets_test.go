//go:build linux

// adr: 20261022185329
package main

import (
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
	for _, manifest := range []api.BuildManifest{
		{Framework: api.FrameworkDockerfile},
		{Framework: api.FrameworkDockerfile, DockerfileBuild: &api.DockerfileBuildOptions{Target: "runtime"}},
		{Framework: api.FrameworkRailpackNode, DockerfileBuild: &api.DockerfileBuildOptions{Secrets: []api.DockerfileBuildSecret{{ID: "npm", Secret: "NPM_TOKEN"}}}},
	} {
		if env, err := buildSecretEnv(manifest); err != nil || env != nil {
			t.Fatalf("%+v: env=%q err=%v", manifest, env, err)
		}
	}
}
