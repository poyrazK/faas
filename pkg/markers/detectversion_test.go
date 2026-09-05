package markers

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTarGz is the port of pkg/builderd/detectversion_test.go's
// helper. Builds a gzipped tarball at path whose top-level
// entries are the given files (name → content).
func writeTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write tarball: %v", err)
	}
}

// tempTarball is a convenience for the per-test gzipped tarball
// path lookup. Each test owns its own t.TempDir().
func tempTarball(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "src.tar.gz")
}

func writeWrappedPAXTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name:       "pax_global_header",
		Typeflag:   tar.TypeXGlobalHeader,
		PAXRecords: map[string]string{"comment": "github codeload metadata"},
	}); err != nil {
		t.Fatalf("pax header: %v", err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "repo-deadbeef/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatalf("project directory: %v", err)
	}
	for name, body := range files {
		name = "repo-deadbeef/" + name
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write tarball: %v", err)
	}
}

func TestVersionFromTarball_GitHubCodeloadPAX(t *testing.T) {
	cases := []struct {
		name string
		fw   Framework
		file string
		body string
		want string
	}{
		{name: "node", fw: FrameworkNode, file: ".nvmrc", body: "v22.11.0", want: "22.11.0"},
		{name: "python", fw: FrameworkPython, file: ".python-version", body: "3.13.1", want: "3.13.1"},
		{name: "go", fw: FrameworkGo, file: "go.mod", body: "module example\n\ngo 1.24\n", want: "1.24"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tempTarball(t)
			writeWrappedPAXTarGz(t, path, map[string]string{tc.file: tc.body})
			if got := VersionFromTarball(path, tc.fw); got != tc.want {
				t.Fatalf("VersionFromTarball = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVersionFromTarballAtRoot_WorkspaceMember(t *testing.T) {
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		"package.json":              `{"engines":{"node":">=18.0.0"}}`,
		"apps/api/package.json":     `{"engines":{"node":">=22.11.0"}}`,
		"packages/web/package.json": `{"engines":{"node":">=20.0.0"}}`,
	})
	if got := VersionFromTarballAtRoot(path, FrameworkNode, "apps/api"); got != "22.11.0" {
		t.Fatalf("VersionFromTarballAtRoot = %q, want 22.11.0", got)
	}
}

// seedDirFSWithFiles creates a tempdir + writes the given
// name→content map (forward-slash names) + returns an os.DirFS
// over the root. Used by the VersionFromFS tests.
func seedDirFSWithFiles(t *testing.T, files map[string]string) fs.FS {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return os.DirFS(dir)
}

// VersionFromTarball — ported from pkg/builderd/detectversion_test.go
// (issue #736 / ADR-088). The 18 cases below pin the per-framework
// priority order and the parser edge cases (operators, malformed
// JSON, comments, etc.).

func TestDetectVersion_NodeNvmrc(t *testing.T) {
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		".nvmrc":       "22.11.0",
		"package.json": "{}",
	})
	if got := VersionFromTarball(path, FrameworkNode); got != "22.11.0" {
		t.Errorf("VersionFromTarball = %q, want 22.11.0", got)
	}
}

func TestDetectVersion_NodeNvmrcVPrefix(t *testing.T) {
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		".nvmrc": "v22.11.0",
	})
	if got := VersionFromTarball(path, FrameworkNode); got != "22.11.0" {
		t.Errorf("VersionFromTarball = %q, want 22.11.0", got)
	}
}

func TestDetectVersion_NodeNvmrcComments(t *testing.T) {
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		".nvmrc": "# my project\n22.11.0\n",
	})
	if got := VersionFromTarball(path, FrameworkNode); got != "22.11.0" {
		t.Errorf("VersionFromTarball = %q, want 22.11.0", got)
	}
}

func TestDetectVersion_NodeEnginesNode(t *testing.T) {
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		"package.json": `{"engines":{"node":">=22.11.0"}}`,
	})
	if got := VersionFromTarball(path, FrameworkNode); got != "22.11.0" {
		t.Errorf("VersionFromTarball = %q, want 22.11.0", got)
	}
}

func TestDetectVersion_NodeEnginesCaret(t *testing.T) {
	// ^22.11.0 must strip the caret and return bare "22.11.0".
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		"package.json": `{"engines":{"node":"^22.11.0"}}`,
	})
	if got := VersionFromTarball(path, FrameworkNode); got != "22.11.0" {
		t.Errorf("VersionFromTarball = %q, want 22.11.0", got)
	}
}

func TestDetectVersion_NodeEnginesTilde(t *testing.T) {
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		"package.json": `{"engines":{"node":"~22.11.0"}}`,
	})
	if got := VersionFromTarball(path, FrameworkNode); got != "22.11.0" {
		t.Errorf("VersionFromTarball = %q, want 22.11.0", got)
	}
}

func TestDetectVersion_NodeNvmrcBeatsEngines(t *testing.T) {
	// The priority order is .nvmrc FIRST, then package.json. A
	// disagreement must resolve to .nvmrc.
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		".nvmrc":       "20.10.0",
		"package.json": `{"engines":{"node":">=22.11.0"}}`,
	})
	if got := VersionFromTarball(path, FrameworkNode); got != "20.10.0" {
		t.Errorf("VersionFromTarball = %q, want 20.10.0 (.nvmrc wins)", got)
	}
}

func TestDetectVersion_NodeMissing(t *testing.T) {
	// Parser must degrade to "" on no marker.
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		"package.json": `{}`,
	})
	if got := VersionFromTarball(path, FrameworkNode); got != "" {
		t.Errorf("VersionFromTarball = %q, want \"\"", got)
	}
}

func TestDetectVersion_NodeMalformedJSON(t *testing.T) {
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		"package.json": `{ not valid json }`,
	})
	if got := VersionFromTarball(path, FrameworkNode); got != "" {
		t.Errorf("VersionFromTarball = %q, want \"\"", got)
	}
}

func TestDetectVersion_PythonPythonVersion(t *testing.T) {
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		".python-version": "3.11.0",
	})
	if got := VersionFromTarball(path, FrameworkPython); got != "3.11.0" {
		t.Errorf("VersionFromTarball = %q, want 3.11.0", got)
	}
}

func TestDetectVersion_PythonPythonVersionTwoComponent(t *testing.T) {
	// .python-version commonly writes "3.11" (two-component).
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		".python-version": "3.11",
	})
	if got := VersionFromTarball(path, FrameworkPython); got != "3.11" {
		t.Errorf("VersionFromTarball = %q, want 3.11", got)
	}
}

func TestDetectVersion_PythonPyprojectRequires(t *testing.T) {
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		"pyproject.toml": `[project]\nname = "x"\nrequires-python = ">=3.13"\n`,
	})
	if got := VersionFromTarball(path, FrameworkPython); got != "3.13" {
		t.Errorf("VersionFromTarball = %q, want 3.13", got)
	}
}

func TestDetectVersion_PythonPyprojectRequiresSingleQuote(t *testing.T) {
	// Single-quoted requires-python (TOML-y) must parse too.
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		"pyproject.toml": `requires-python = '>=3.13'`,
	})
	if got := VersionFromTarball(path, FrameworkPython); got != "3.13" {
		t.Errorf("VersionFromTarball = %q, want 3.13", got)
	}
}

func TestDetectVersion_PythonVersionBeatsRequires(t *testing.T) {
	// Priority: .python-version > pyproject.toml::requires-python.
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		".python-version": "3.11",
		"pyproject.toml":  `requires-python = ">=3.13"`,
	})
	if got := VersionFromTarball(path, FrameworkPython); got != "3.11" {
		t.Errorf("VersionFromTarball = %q, want 3.11 (.python-version wins)", got)
	}
}

func TestDetectVersion_GoModDirect(t *testing.T) {
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		"go.mod": "module x\n\ngo 1.24\n",
	})
	if got := VersionFromTarball(path, FrameworkGo); got != "1.24" {
		t.Errorf("VersionFromTarball = %q, want 1.24", got)
	}
}

func TestDetectVersion_GoModIndirect(t *testing.T) {
	// A "go 1.24" line in a go.mod with a leading require
	// block must still be parsed.
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		"go.mod": "module x\n\nrequire (\n\tyaml.v3 v3.0.0\n)\n\ngo 1.24\n",
	})
	if got := VersionFromTarball(path, FrameworkGo); got != "1.24" {
		t.Errorf("VersionFromTarball = %q, want 1.24", got)
	}
}

func TestDetectVersion_GoModMissingReturnsEmpty(t *testing.T) {
	// A go.mod without a "go X.Y" directive (rare) returns "".
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		"go.mod": "module x\n",
	})
	if got := VersionFromTarball(path, FrameworkGo); got != "" {
		t.Errorf("VersionFromTarball = %q, want \"\"", got)
	}
}

func TestDetectVersion_DockerIsEmpty(t *testing.T) {
	// Docker is explicitly out-of-scope. The parser returns ""
	// regardless of what files exist.
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		"Dockerfile": "FROM alpine:3.20",
		".nvmrc":     "22.11.0",
	})
	if got := VersionFromTarball(path, FrameworkDocker); got != "" {
		t.Errorf("VersionFromTarball = %q, want \"\" (docker out-of-scope)", got)
	}
}

func TestDetectVersion_UnknownIsEmpty(t *testing.T) {
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		".nvmrc": "22.11.0",
	})
	if got := VersionFromTarball(path, FrameworkUnknown); got != "" {
		t.Errorf("VersionFromTarball = %q, want \"\" (unknown framework)", got)
	}
}

// VersionFromFS — primary CLI-side input. Pins behaviour without
// a tarball round-trip.

func TestVersionFromFS_NodeNvmrc(t *testing.T) {
	fsys := seedDirFSWithFiles(t, map[string]string{
		".nvmrc":       "22.11.0",
		"package.json": "{}",
	})
	if got := VersionFromFS(fsys, FrameworkNode); got != "22.11.0" {
		t.Errorf("VersionFromFS = %q, want 22.11.0", got)
	}
}

func TestVersionFromFS_NodeEnginesCaret(t *testing.T) {
	fsys := seedDirFSWithFiles(t, map[string]string{
		"package.json": `{"engines":{"node":"^22.11.0"}}`,
	})
	if got := VersionFromFS(fsys, FrameworkNode); got != "22.11.0" {
		t.Errorf("VersionFromFS = %q, want 22.11.0", got)
	}
}

func TestVersionFromFS_PythonPythonVersion(t *testing.T) {
	fsys := seedDirFSWithFiles(t, map[string]string{
		".python-version": "3.11.0",
	})
	if got := VersionFromFS(fsys, FrameworkPython); got != "3.11.0" {
		t.Errorf("VersionFromFS = %q, want 3.11.0", got)
	}
}

func TestVersionFromFS_PythonPyprojectRequires(t *testing.T) {
	fsys := seedDirFSWithFiles(t, map[string]string{
		"pyproject.toml": `requires-python = ">=3.13"`,
	})
	if got := VersionFromFS(fsys, FrameworkPython); got != "3.13" {
		t.Errorf("VersionFromFS = %q, want 3.13", got)
	}
}

func TestVersionFromFS_GoModDirective(t *testing.T) {
	fsys := seedDirFSWithFiles(t, map[string]string{
		"go.mod": "module x\n\ngo 1.24\n",
	})
	if got := VersionFromFS(fsys, FrameworkGo); got != "1.24" {
		t.Errorf("VersionFromFS = %q, want 1.24", got)
	}
}

func TestVersionFromFS_EmptyWhenNoMarkers(t *testing.T) {
	fsys := seedDirFSWithFiles(t, map[string]string{
		"README.md": "# x\n",
	})
	if got := VersionFromFS(fsys, FrameworkNode); got != "" {
		t.Errorf("VersionFromFS = %q, want \"\"", got)
	}
}

func TestVersionFromFS_EmptyWhenMalformed(t *testing.T) {
	fsys := seedDirFSWithFiles(t, map[string]string{
		"package.json": `{ not valid json }`,
	})
	if got := VersionFromFS(fsys, FrameworkNode); got != "" {
		t.Errorf("VersionFromFS = %q, want \"\"", got)
	}
}

func TestVersionFromFS_OversizedFile(t *testing.T) {
	// A .nvmrc above maxVersionFileBytes (64 KB) is treated as
	// missing — refuses the OOM-by-source attack.
	big := strings.Repeat("a", maxVersionFileBytes+1)
	fsys := seedDirFSWithFiles(t, map[string]string{
		".nvmrc": big,
	})
	if got := VersionFromFS(fsys, FrameworkNode); got != "" {
		t.Errorf("VersionFromFS = %q, want \"\" (oversized file)", got)
	}
}

func TestVersionFromFS_DockerIsEmpty(t *testing.T) {
	fsys := seedDirFSWithFiles(t, map[string]string{
		"Dockerfile": "FROM alpine:3.20\n",
	})
	if got := VersionFromFS(fsys, FrameworkDocker); got != "" {
		t.Errorf("VersionFromFS = %q, want \"\" (docker out-of-scope)", got)
	}
}

func TestVersionFromFS_UnknownIsEmpty(t *testing.T) {
	fsys := seedDirFSWithFiles(t, map[string]string{
		".nvmrc": "22.11.0",
	})
	if got := VersionFromFS(fsys, FrameworkUnknown); got != "" {
		t.Errorf("VersionFromFS = %q, want \"\" (unknown framework)", got)
	}
}

// Markers() does not need an FS, but the priority slice is
// load-bearing — pin it here too.
func TestVersionFromFS_DoesNotConsumeContainerisedFiles(t *testing.T) {
	// A nested .nvmrc under apps/web must NOT be picked up —
	// the FS path mirrors the top-level-only server rule.
	fsys := seedDirFSWithFiles(t, map[string]string{
		"apps/web/.nvmrc": "22.11.0",
	})
	if got := VersionFromFS(fsys, FrameworkNode); got != "" {
		t.Errorf("VersionFromFS = %q, want \"\" (nested .nvmrc must not count)", got)
	}
}

// Read helpers — silent error contract. The CLI and server
// both treat parse failures as "missing", so errors are
// intentionally swallowed.

func TestReadFSFile_MissingReturnsEmpty(t *testing.T) {
	fsys := seedDirFSWithFiles(t, nil)
	if got := readFSFile(fsys, "does-not-exist"); got != "" {
		t.Errorf("readFSFile(missing) = %q, want \"\"", got)
	}
}

func TestReadFSFile_LargeFileReturnsEmpty(t *testing.T) {
	// File present but bigger than maxVersionFileBytes => "".
	dir := t.TempDir()
	big := strings.Repeat("a", maxVersionFileBytes+1)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readFSFile(os.DirFS(dir), "big.txt"); got != "" {
		t.Errorf("readFSFile(large) = %q (len %d), want \"\"", got[:8], len(got))
	}
}

func TestReadTarballFile_MissingReturnsEmpty(t *testing.T) {
	path := tempTarball(t)
	writeTarGz(t, path, map[string]string{
		"package.json": "{}",
	})
	if got := readTarballFile(path, "no-such-file"); got != "" {
		t.Errorf("readTarballFile(missing) = %q, want \"\"", got)
	}
}

func TestReadTarballFile_MalformedTarball(t *testing.T) {
	// A bad tarball returns "" — error swallowing is the
	// documented contract.
	path := filepath.Join(t.TempDir(), "bad.tar.gz")
	if err := os.WriteFile(path, []byte("not a tarball"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readTarballFile(path, ".nvmrc"); got != "" {
		t.Errorf("readTarballFile(bad) = %q, want \"\"", got)
	}
}
