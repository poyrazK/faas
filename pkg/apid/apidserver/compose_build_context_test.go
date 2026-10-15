package apidserver_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// H10-25: Compose `build: ./web` with `COPY server.js .` failed on prod with
// "/server.js: not found" because the builder used the repository as the
// Docker build context. A Compose Dockerfile service now stages its own
// directory as the archive root.
func TestComposeDockerBuildContext(t *testing.T) {
	scanDir := t.TempDir()
	for name, body := range map[string]string{
		"package.json":        `{"name":"root"}`,
		"web/Dockerfile":      "FROM node:22-alpine\nCOPY server.js .\n",
		"web/server.js":       "require('http')",
		"api/Dockerfile.prod": "FROM scratch\n",
		"worker/package.json": `{"name":"worker"}`,
		"worker/worker.js":    "setInterval(() => {}, 1000)",
	} {
		path := filepath.Join(scanDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	compose := reposcan.Workload{DetectedBy: reposcan.Detection{Detector: reposcan.DetectorCompose}}
	app := func(root, dockerfile, image string) state.App {
		a := state.App{RootDir: root}
		a.Manifest.BuildDockerfile, a.Manifest.ProjectImage = dockerfile, image
		return a
	}
	for _, tc := range []struct {
		name     string
		workload reposcan.Workload
		app      state.App
		want     bool
	}{
		{"compose service directory", compose, app("web", "", ""), true},
		{"compose explicit dockerfile", compose, app("api", "Dockerfile.prod", ""), true},
		{"compose root context", compose, app("", "", ""), false},
		{"compose without Dockerfile keeps repository", compose, app("worker", "", ""), false},
		{"compose image service", compose, app("web", "", "nginx:1.27"), false},
		// A Dockerfile found beside the Compose file, not declared in it,
		// keeps the repository even in a compose-sourced project.
		{"undeclared Dockerfile keeps repository (ADR-144)", reposcan.Workload{DetectedBy: reposcan.Detection{Detector: "other"}}, app("web", "", ""), false},
	} {
		if got := apidserver.ComposeDockerBuildContext(scanDir, tc.workload, tc.app); got != tc.want {
			t.Errorf("%s: composeDockerBuildContext = %v, want %v", tc.name, got, tc.want)
		}
	}

	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	for _, tc := range []struct {
		composeContext bool
		want           []string
	}{
		{true, []string{"Dockerfile", "server.js"}},
		{false, []string{"api/Dockerfile.prod", "package.json", "web/Dockerfile", "web/server.js", "worker/package.json", "worker/worker.js"}},
	} {
		staged, _, err := apidserver.StageApplyTarball(t.Context(), scanDir, "acct", "proj", app("web", "", ""), tc.composeContext)
		if err != nil {
			t.Fatal(err)
		}
		if got := stagedFiles(t, staged); !slices.Equal(got, tc.want) {
			t.Errorf("composeContext=%v staged %v, want %v", tc.composeContext, got, tc.want)
		}
	}
}

func stagedFiles(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			files = append(files, filepath.ToSlash(filepath.Clean(h.Name)))
		}
	}
	slices.Sort(files)
	return files
}
