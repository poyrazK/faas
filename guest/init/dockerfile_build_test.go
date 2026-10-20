//go:build linux

package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDockerfileBuildOptionsReachBuildctl(t *testing.T) {
	options := &api.DockerfileBuildOptions{
		Target: "runtime",
		Args:   map[string]string{"NODE_ENV": "production", "VITE_API_URL": "http://from-compose", "EMPTY": "", "SPACED": "a b; $(x)"},
	}
	argv := buildArgv(api.BuildManifest{
		Framework: api.FrameworkDockerfile, Workdir: "/build/src", OutDir: "/build/out", DockerfilePath: "Dockerfile.prod",
		DockerfileBuild: options, PublicBuildURLs: map[string]string{"VITE_API_URL": "https://api.example.com"},
	})
	opts := buildctlOpts(argv)
	want := []string{
		"filename=Dockerfile.prod",
		"target=runtime",
		"build-arg:EMPTY=",
		"build-arg:NODE_ENV=production",
		"build-arg:SPACED=a b; $(x)",
		"build-arg:VITE_API_URL=https://api.example.com",
	}
	if !slices.Equal(opts, want) {
		t.Fatalf("buildctl --opt values = %q, want %q", opts, want)
	}
	for _, arg := range argv {
		if strings.Contains(arg, "from-compose") {
			t.Fatalf("a reviewed public build URL must replace the Compose argument: %q", argv)
		}
	}
}

func TestDockerfileBuildWithoutOptionsKeepsLegacyArgv(t *testing.T) {
	manifest := api.BuildManifest{Framework: api.FrameworkDockerfile, Workdir: "/build/src", OutDir: "/build/out"}
	legacy := buildArgv(manifest)
	manifest.DockerfileBuild = &api.DockerfileBuildOptions{}
	if got := buildArgv(manifest); !slices.Equal(got, legacy) {
		t.Fatalf("empty options changed argv: %q -> %q", legacy, got)
	}
	if opts := buildctlOpts(legacy); len(opts) != 0 {
		t.Fatalf("legacy Dockerfile argv has options %q", opts)
	}
}

func TestDockerfileBuildAcknowledgement(t *testing.T) {
	options := &api.DockerfileBuildOptions{Target: "runtime", Args: map[string]string{"A": "1"}}
	for _, tc := range []struct {
		name     string
		manifest api.BuildManifest
		want     string
	}{
		{"dockerfile with options", api.BuildManifest{Framework: api.FrameworkDockerfile, DockerfileBuild: options}, options.SHA256()},
		{"dockerfile without options", api.BuildManifest{Framework: api.FrameworkDockerfile}, ""},
		{"railpack never applies options", api.BuildManifest{Framework: api.FrameworkRailpackNode, DockerfileBuild: options}, ""},
	} {
		if got := dockerfileBuildAcknowledgement(tc.manifest); got != tc.want {
			t.Errorf("%s: acknowledgement = %q, want %q", tc.name, got, tc.want)
		}
	}
	if options.SHA256() == "" {
		t.Fatal("options must have a digest")
	}
}

// buildctlOpts returns the values of every --opt flag, in order.
func buildctlOpts(argv []string) []string {
	var opts []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "--opt" {
			opts = append(opts, argv[i+1])
		}
	}
	return opts
}
