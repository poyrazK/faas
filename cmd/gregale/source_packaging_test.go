package main_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261026120000 source packaging: overridable defaults, Dockerfile
// contexts, dereferenced symlinks, submodules, Git LFS and the server's
// size limits.

// The resumable session cannot carry an MCP task migration override, so a
// deploy that sets it uses multipart instead of dropping it.
func TestMCPTaskMigrationOverrideSkipsResumableUpload(t *testing.T) {
	if !main.CanUseResumableUpload(main.ShapeApp, "", "", false, "", api.DeployAnnotations{}, -1, "", "") {
		t.Fatal("plain app deploy should use the resumable upload")
	}
	if main.CanUseResumableUpload(main.ShapeApp, "", "", false, "", api.DeployAnnotations{MCPTaskMigrationOverride: true}, -1, "", "") {
		t.Fatal("MCP task migration override must use multipart")
	}
}

func writeSourceFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// archiveFiles returns regular-file contents keyed by name with the archive
// root directory stripped.
func archiveFiles(t *testing.T, archive string) map[string]string {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeSymlink {
			t.Fatalf("archive kept symlink %s", hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[hdr.Name] = string(body)
	}
}

func packSource(t *testing.T, dir string, capMB int) (map[string]string, error) {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "source.tar.gz")
	if _, err := main.PackDirToTarGz(dir, dest, capMB, nil); err != nil {
		return nil, err
	}
	files := archiveFiles(t, dest)
	stripped := make(map[string]string, len(files))
	for name, body := range files {
		stripped[strings.TrimPrefix(name, filepath.Base(dir)+"/")] = body
	}
	return stripped, nil
}

func TestPackDefaultsAreOverridable(t *testing.T) {
	dir := t.TempDir()
	writeSourceFiles(t, dir, map[string]string{
		"package.json":              "{}",
		"dist/app.js":               "built",
		"node_modules/x/i.js":       "dep",
		"vendor/modules.txt":        "# example.com/m v1.0.0\n",
		"vendor/example.com/m/m.go": "package m",
		".git/HEAD":                 "ref: refs/heads/main",
		".gregaleignore":            "!dist/\n!.git/\n",
	})
	files, err := packSource(t, dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"dist/app.js", "vendor/example.com/m/m.go", "package.json"} {
		if _, ok := files[want]; !ok {
			t.Errorf("%s missing from archive", want)
		}
	}
	for _, unwanted := range []string{"node_modules/x/i.js", ".git/HEAD"} {
		if _, ok := files[unwanted]; ok {
			t.Errorf("%s shipped", unwanted)
		}
	}
}

func TestPackDockerfileContext(t *testing.T) {
	dir := t.TempDir()
	writeSourceFiles(t, dir, map[string]string{
		"Dockerfile":        "FROM scratch\nCOPY target/app.jar /app.jar\n",
		"target/app.jar":    "jar",
		"node_modules/x.js": "dep",
		"local/secrets.txt": "keep-out",
		".dockerignore":     "local\n",
	})
	files, err := packSource(t, dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["target/app.jar"]; !ok {
		t.Error("Dockerfile build output target/app.jar missing")
	}
	for _, unwanted := range []string{"node_modules/x.js", "local/secrets.txt"} {
		if _, ok := files[unwanted]; ok {
			t.Errorf("%s shipped", unwanted)
		}
	}
}

func TestPackDereferencesInTreeSymlinks(t *testing.T) {
	dir := t.TempDir()
	writeSourceFiles(t, dir, map[string]string{"package.json": "{}", "shared/c.js": "shared"})
	if err := os.Symlink("shared/c.js", filepath.Join(dir, "link.js")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("shared", filepath.Join(dir, "lib")); err != nil {
		t.Fatal(err)
	}
	files, err := packSource(t, dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	if files["link.js"] != "shared" || files["lib/c.js"] != "shared" {
		t.Fatalf("symlinks not dereferenced: link.js=%q lib/c.js=%q", files["link.js"], files["lib/c.js"])
	}

	outside := t.TempDir()
	if err := os.Symlink(filepath.Join(outside), filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := packSource(t, dir, 100); err == nil || !strings.Contains(err.Error(), "outside the source directory") {
		t.Fatalf("escaping symlink err = %v", err)
	}
}

func TestPackLimitsMatchServer(t *testing.T) {
	dir := t.TempDir()
	// 3 MB that compresses to a few KB fits a 1 MB upload limit.
	writeSourceFiles(t, dir, map[string]string{"package.json": "{}", "assets/zeros.bin": strings.Repeat("\x00", 3<<20)})
	if _, err := packSource(t, dir, 1); err != nil {
		t.Fatalf("compressible source refused: %v", err)
	}
	noise := make([]byte, 2<<20)
	if _, err := rand.Read(noise); err != nil {
		t.Fatal(err)
	}
	writeSourceFiles(t, dir, map[string]string{"assets/noise.bin": string(noise)})
	if _, err := packSource(t, dir, 1); err == nil || !strings.Contains(err.Error(), "1 MB upload limit") {
		t.Fatalf("incompressible source err = %v, want the upload limit", err)
	}
}

func gitOrSkip(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git %v: %v: %s", args, err, out)
	}
}

func TestNormalizeGitArchive(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	app := filepath.Join(root, "app")
	writeSourceFiles(t, sub, map[string]string{"lib.js": "from submodule"})
	gitOrSkip(t, sub, "init", "-q")
	gitOrSkip(t, sub, "add", ".")
	gitOrSkip(t, sub, "commit", "-qm", "sub")

	asset := "real lfs bytes"
	sum := sha256.Sum256([]byte(asset))
	oid := hex.EncodeToString(sum[:])
	writeSourceFiles(t, app, map[string]string{
		"package.json": "{}",
		"shared/c.js":  "shared",
		"assets/a.bin": fmt.Sprintf("version https://git-lfs.github.com/spec/v1\noid sha256:%s\nsize %d\n", oid, len(asset)),
		"assets/b.bin": "version https://git-lfs.github.com/spec/v1\noid sha256:" + strings.Repeat("ab", 32) + "\nsize 3\n",
	})
	gitOrSkip(t, app, "init", "-q")
	if err := os.Symlink("shared/c.js", filepath.Join(app, "link.js")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("shared", filepath.Join(app, "lib")); err != nil {
		t.Fatal(err)
	}
	gitOrSkip(t, app, "-c", "protocol.file.allow=always", "submodule", "--quiet", "add", sub, "libs/sub")
	gitOrSkip(t, app, "-c", "protocol.file.allow=always", "submodule", "--quiet", "add", sub, "libs/other")
	gitOrSkip(t, app, "add", ".")
	gitOrSkip(t, app, "commit", "-qm", "app")
	gitOrSkip(t, app, "submodule", "--quiet", "deinit", "-f", "libs/other")
	writeSourceFiles(t, filepath.Join(app, ".git", "lfs", "objects", oid[:2], oid[2:4]), map[string]string{oid: asset})

	archive := filepath.Join(t.TempDir(), "head.tar.gz")
	if err := main.GitArchiveHEAD(app, archive); err != nil {
		t.Fatal(err)
	}
	warnings, err := main.NormalizeGitArchive(app, "", archive)
	if err != nil {
		t.Fatal(err)
	}
	files := archiveFiles(t, archive)
	for name, want := range map[string]string{
		"link.js": "shared", "lib/c.js": "shared", "libs/sub/lib.js": "from submodule", "assets/a.bin": asset,
	} {
		if files[name] != want {
			t.Errorf("%s = %q, want %q", name, files[name], want)
		}
	}
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"libs/other is not checked out", "assets/b.bin", "git lfs pull"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q missing %q", joined, want)
		}
	}
	if bytes.Contains([]byte(joined), []byte("libs/sub")) {
		t.Errorf("initialized submodule reported: %q", joined)
	}
}
