package frameworkprofile

import (
	"reflect"
	"testing"
)

func TestDockerfileRegistryScope(t *testing.T) {
	archive := writeProfileArchive(t, map[string]string{
		"Dockerfile":            "ARG BASE=ghcr.io/acme/base\nFROM ${BASE}:1\n",
		"deploy/api.Dockerfile": "FROM node:22\nCOPY --from=quay.io/acme/assets / /\n",
		"web/Containerfile":     "FROM ghcr.io/acme/web\n",
		"docker/build.df":       "FROM ghcr.io/acme/explicit\n",
		"docs/README.md":        "FROM ghcr.io/not/a/dockerfile\n",
	})
	got, found, err := DockerfileRegistryScope(archive, "", []string{"./docker/build.df"}, map[string]string{"BASE": "ghcr.io/acme/override"})
	if err != nil || !found {
		t.Fatalf("scope = %v, found %v, %v", got, found, err)
	}
	want := []string{"docker.io/library/node", "ghcr.io/acme/explicit", "ghcr.io/acme/override", "ghcr.io/acme/web", "quay.io/acme/assets"}
	if !reflect.DeepEqual(got.Repositories, want) {
		t.Fatalf("repositories = %v, want %v", got.Repositories, want)
	}

	plain := writeProfileArchive(t, map[string]string{"package.json": `{"name":"x"}`})
	if got, found, err := DockerfileRegistryScope(plain, "", nil, nil); err != nil || found || len(got.Repositories) != 0 {
		t.Fatalf("no Dockerfile = %v, found %v, %v", got, found, err)
	}
}

func TestIsDockerfileName(t *testing.T) {
	for name, want := range map[string]bool{
		"Dockerfile": true, "dockerfile": true, "Containerfile": true, "Dockerfile.prod": true, "api.Dockerfile": true,
		"Dockerfile.md": true, "Dockerfiles": false, "README.md": false, "docker-compose.yml": false,
	} {
		if got := IsDockerfileName(name); got != want {
			t.Errorf("IsDockerfileName(%q) = %v, want %v", name, got, want)
		}
	}
}
