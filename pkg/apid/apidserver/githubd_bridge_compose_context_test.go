package apidserver_test

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// writeRepoTarball writes files as the whole-repository archive githubd
// stages.
func writeRepoTarball(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []io.Closer{tw, gz, f} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// tarballFiles lists the regular files of a .tar.gz.
func tarballFiles(t *testing.T, path string) map[string]bool {
	t.Helper()
	f, err := os.Open(path) //nolint:forbidigo // test archive the test just wrote
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	files := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			files[strings.TrimPrefix(h.Name, "./")] = true
		}
	}
}

// writeTarGz writes headers to path; a regular file's body is Size "x"s.
func writeTarGz(t *testing.T, path string, headers []tar.Header) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, h := range headers {
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := io.WriteString(tw, strings.Repeat("x", int(h.Size))); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, c := range []io.Closer{tw, gz, f} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// tarballEntries maps each entry name of a .tar.gz to its link target.
func tarballEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path) //nolint:forbidigo // test archive the test just wrote
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	entries := map[string]string{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}
		if err != nil {
			t.Fatal(err)
		}
		entries[h.Name] = h.Linkname
	}
}

// A GitHub build of a Compose service built from a Dockerfile in its own
// directory gets that directory as the archive root, as a project deploy
// stages it (trial 2026-11-28: `build: ./web` failed on GitHub with
// "/app.py": not found). Anything else keeps the repository.
func TestGitHubBuildNarrowsToComposeBuildContext(t *testing.T) {
	t.Setenv(apidserver.ScanSpoolRootEnv, t.TempDir())
	repo := map[string]string{
		"compose.yaml":   "services:\n  web:\n    build: ./web\n",
		"README.md":      "trial\n",
		"web/Dockerfile": "FROM python:3.12-slim\nCOPY app.py /app.py\n",
		"web/app.py":     "print('hi')\n",
	}
	source := writeRepoTarball(t, repo)
	compose := state.App{ID: "web", RootDir: "web"}
	compose.Manifest.BuildComposeContext = true

	spooled, narrowed, err := apidserver.SpoolBuildSource(t.Context(), t.TempDir(), source, compose)
	if err != nil || narrowed <= 0 {
		t.Fatalf("spool compose service = %q, %d, %v; want a narrowed archive", spooled, narrowed, err)
	}
	if info, err := os.Stat(spooled); err != nil || info.Size() != narrowed {
		t.Fatalf("narrowed archive size = %v, %v; want %d", info, err, narrowed)
	}
	files := tarballFiles(t, spooled)
	if !files["Dockerfile"] || !files["app.py"] || files["compose.yaml"] || files["README.md"] || files["web/app.py"] {
		t.Fatalf("narrowed archive = %v; want the web directory at its root", files)
	}

	// A "./" prefixed archive with links: a symlink stays, a hard link
	// within the context is re-rooted, one to a file outside it is dropped.
	linked := filepath.Join(t.TempDir(), "linked.tar.gz")
	writeTarGz(t, linked, []tar.Header{
		{Name: "./web/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "./web/Dockerfile", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4},
		{Name: "./web/app.py", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4},
		{Name: "./web/current", Typeflag: tar.TypeSymlink, Linkname: "app.py"},
		{Name: "./web/same.py", Typeflag: tar.TypeLink, Linkname: "./web/app.py"},
		{Name: "./web/shared.py", Typeflag: tar.TypeLink, Linkname: "./README.md"},
		{Name: "./README.md", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4},
	})
	spooled, narrowed, err = apidserver.SpoolBuildSource(t.Context(), t.TempDir(), linked, compose)
	if err != nil || narrowed <= 0 {
		t.Fatalf("spool linked = %q, %d, %v; want a narrowed archive", spooled, narrowed, err)
	}
	entries := tarballEntries(t, spooled)
	if _, ok := entries["Dockerfile"]; !ok || entries["current"] != "app.py" || entries["same.py"] != "app.py" {
		t.Fatalf("narrowed links = %v; want Dockerfile, the symlink kept and the hard link re-rooted", entries)
	}
	if _, ok := entries["shared.py"]; ok {
		t.Fatalf("narrowed links = %v; want the hard link to a file outside the context dropped", entries)
	}

	for name, app := range map[string]state.App{
		"not a compose build": {ID: "web", RootDir: "web"},
		"repository root":     {ID: "root", Manifest: state.AppManifest{BuildComposeContext: true}},
		"no Dockerfile there": {ID: "api", RootDir: "api", Manifest: state.AppManifest{BuildComposeContext: true}},
		"image service":       {ID: "proxy", RootDir: "web", Manifest: state.AppManifest{BuildComposeContext: true, ProjectImage: "nginx:1.27"}},
	} {
		spooled, narrowed, err := apidserver.SpoolBuildSource(t.Context(), t.TempDir(), source, app)
		if err != nil || narrowed != 0 {
			t.Fatalf("%s: spool = %q, %d, %v; want the repository unchanged", name, spooled, narrowed, err)
		}
		if files := tarballFiles(t, spooled); !files["compose.yaml"] || !files["web/app.py"] {
			t.Fatalf("%s: spooled archive = %v; want the whole repository", name, files)
		}
	}
}
