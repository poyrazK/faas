package markers

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// makeTarball produces a tarball at path whose root contains the
// given filenames (used to seed detector fixtures). Empty
// content is fine.
func makeTarball(t *testing.T, path string, names []string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, n := range names {
		hdr := &tar.Header{Name: n, Mode: 0o644, Size: 0, Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedDirFS seeds the given filenames (empty content) into a
// t.TempDir() and returns it as an fs.FS via os.DirFS. Used by
// the FS-path tests.
func seedDirFS(t *testing.T, files []string) fs.FS {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return os.DirFS(dir)
}

// DetectFromTarball — ported from pkg/builderd/detect_test.go
// (issue #736 / ADR-088). The 8 cases below pin the priority
// order: Docker > Node > Python > Go.

func TestDetectFromTarball_Node(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	makeTarball(t, path, []string{"package.json", "index.js", "lib/util.js"})

	got, err := DetectFromTarball(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkNode {
		t.Errorf("framework = %s, want node", got)
	}
}

func TestDetectFromTarballAtRoot_WorkspaceMember(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	makeTarball(t, path, []string{
		"package.json",
		"apps/api/package.json",
		"apps/api/index.js",
		"packages/shared/package.json",
	})

	got, err := DetectFromTarballAtRoot(path, "apps/api")
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkNode {
		t.Fatalf("framework = %s, want node for selected workspace member", got)
	}
	got, err = DetectFromTarballAtRoot(path, "packages/missing")
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkUnknown {
		t.Fatalf("framework = %s, want unknown for missing workspace member", got)
	}
}

func TestDetectFromTarballAtRoot_WrappedWorkspaceMember(t *testing.T) {
	path := filepath.Join(t.TempDir(), "src.tar.gz")
	writeWrappedPAXTarGz(t, path, map[string]string{
		"apps/api/package.json": "{}",
	})

	got, err := DetectFromTarballAtRoot(path, "apps/api")
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkNode {
		t.Fatalf("framework = %s, want node below transport wrapper", got)
	}
}

func TestDetectFromTarball_PackedTopLevelDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	makeTarball(t, path, []string{"app/package.json", "app/index.js"})

	got, err := DetectFromTarball(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkNode {
		t.Errorf("framework = %s, want node for a single packed directory prefix", got)
	}
}

func TestDetectFromTarball_IgnoresPAXGlobalHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		PAXRecords: map[string]string{"comment": "git archive"},
		Typeflag:   tar.TypeXGlobalHeader,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name:     "cron-shot-commit/package.json",
		Mode:     0o644,
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := DetectFromTarball(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkNode {
		t.Fatalf("framework = %s, want node for a GitHub codeload archive", got)
	}
}

func TestDetectFromTarball_Python(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	makeTarball(t, path, []string{"requirements.txt", "main.py"})

	got, err := DetectFromTarball(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkPython {
		t.Errorf("framework = %s, want python", got)
	}
}

func TestDetectFromTarball_DockerfileWins(t *testing.T) {
	// A Dockerfile at the root wins over package.json — matches
	// the user experience of `faas deploy --dockerfile`.
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	makeTarball(t, path, []string{"Dockerfile", "package.json"})

	got, err := DetectFromTarball(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkDocker {
		t.Errorf("framework = %s, want docker", got)
	}
}

func TestDetectFromTarball_Go(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	makeTarball(t, path, []string{"go.mod", "main.go", "internal/server.go"})

	got, err := DetectFromTarball(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkGo {
		t.Errorf("framework = %s, want go", got)
	}
}

func TestDetectFromTarball_DockerfileBeatsGo(t *testing.T) {
	// A root Dockerfile wins over go.mod — mirrors the user
	// expectation of `faas deploy --dockerfile` taking
	// precedence over a coincidental go.mod that lives in a Go
	// project that ALSO ships a Dockerfile.
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	makeTarball(t, path, []string{"Dockerfile", "go.mod", "main.go"})

	got, err := DetectFromTarball(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkDocker {
		t.Errorf("framework = %s, want docker (Dockerfile wins over go.mod)", got)
	}
}

func TestDetectFromTarball_PythonBeatsGo(t *testing.T) {
	// Defensive priority pin: a root go.mod alongside a
	// coincidental requirements.txt must resolve to python, not
	// go. The order is docker > node > python > go (python is
	// checked before go in the detector's switch); this is
	// intentional because a requirements.txt alongside a go.mod
	// most likely indicates a polyglot project where the Python
	// side is the primary deploy target.
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	makeTarball(t, path, []string{"go.mod", "requirements.txt", "main.go", "app.py"})

	got, err := DetectFromTarball(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkPython {
		t.Errorf("framework = %s, want python (priority pin: python wins over go when both markers are root-level)", got)
	}
}

func TestDetectFromTarball_Unknown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	makeTarball(t, path, []string{"README.md", "src/main.c"})

	got, err := DetectFromTarball(path)
	if err != nil {
		t.Errorf("DetectFromTarball(unknown) error = %v, want nil (parity contract)", err)
	}
	if got != FrameworkUnknown {
		t.Errorf("framework = %s, want unknown (no marker present)", got)
	}
}

func TestDetectFromTarball_NestedEntriesIgnored(t *testing.T) {
	// package.json buried in a subdir is NOT a project-level
	// package.json.
	dir := t.TempDir()
	path := filepath.Join(dir, "src.tar.gz")
	makeTarball(t, path, []string{"subdir/package.json", "requirements.txt"})

	got, err := DetectFromTarball(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkPython {
		t.Errorf("framework = %s, want python (top-level wins)", got)
	}
}

func TestDetectFromTarball_BadTarball(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.tar.gz")
	if err := os.WriteFile(path, []byte("not a tarball"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DetectFromTarball(path); err == nil {
		t.Error("expected error on malformed tarball")
	}
}

func TestDetectFromTarball_MissingFile(t *testing.T) {
	if _, err := DetectFromTarball("/no/such/file.tar.gz"); err == nil {
		t.Error("expected error on missing file")
	}
}

// DetectFromFS — the CLI's primary input. Pins behaviour
// without a tarball round-trip.

func TestDetectFromFS_Node(t *testing.T) {
	fsys := seedDirFS(t, []string{"package.json", "index.js"})
	got, err := DetectFromFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkNode {
		t.Errorf("framework = %s, want node", got)
	}
}

func TestDetectFromFS_Python(t *testing.T) {
	fsys := seedDirFS(t, []string{"requirements.txt", "main.py"})
	got, err := DetectFromFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkPython {
		t.Errorf("framework = %s, want python", got)
	}
}

func TestDetectFromFS_Go(t *testing.T) {
	fsys := seedDirFS(t, []string{"go.mod", "main.go"})
	got, err := DetectFromFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkGo {
		t.Errorf("framework = %s, want go", got)
	}
}

func TestDetectFromFS_DockerfileCaseInsensitive(t *testing.T) {
	// Server side already pinned case-insensitive matching; this
	// pins the FS path too. A customer who writes `dockerfile`
	// (lowercase) on macOS / Windows / case-insensitive APFS
	// must still resolve to Docker.
	fsys := seedDirFS(t, []string{"dockerfile"})
	got, err := DetectFromFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkDocker {
		t.Errorf("framework = %s, want docker (lowercase)", got)
	}
}

func TestDetectFromFS_PackageJsonCaseInsensitive(t *testing.T) {
	fsys := seedDirFS(t, []string{"Package.JSON"})
	got, err := DetectFromFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkNode {
		t.Errorf("framework = %s, want node (mixed-case package.json)", got)
	}
}

func TestDetectFromFS_Empty(t *testing.T) {
	fsys := seedDirFS(t, nil)
	got, err := DetectFromFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkUnknown {
		t.Errorf("framework = %s, want unknown (empty dir)", got)
	}
}

func TestDetectFromFS_NestedIgnored(t *testing.T) {
	// A package.json buried in a subdir must NOT be detected.
	fsys := seedDirFS(t, []string{"apps/web/package.json", "README.md"})
	got, err := DetectFromFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkUnknown {
		t.Errorf("framework = %s, want unknown (nested package.json must not count)", got)
	}
}

func TestDetectFromFS_HandlerAloneNotMarker(t *testing.T) {
	// handler.js is a CLI shape concern (function mode), NOT an
	// app marker. The server must NOT treat it as a Node project.
	fsys := seedDirFS(t, []string{"handler.js"})
	got, err := DetectFromFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkUnknown {
		t.Errorf("framework = %s, want unknown (handler.* is not a marker)", got)
	}
}

func TestDetectFromFS_DockerfileWinsOverNode(t *testing.T) {
	// Priority pin: Dockerfile must win over package.json.
	fsys := seedDirFS(t, []string{"Dockerfile", "package.json"})
	got, err := DetectFromFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if got != FrameworkDocker {
		t.Errorf("framework = %s, want docker", got)
	}
}

func TestDetectFromFS_MissingDir(t *testing.T) {
	// os.DirFS on a non-existent path returns a non-nil error
	// when reading "." — the caller (CLI) handles it.
	fsys := os.DirFS("/no/such/directory/anywhere")
	if _, err := DetectFromFS(fsys); err == nil {
		t.Error("expected error on missing directory")
	}
}

// MarkerFor / IsAppMarker / Markers — single-name lookups.

func TestMarkerFor_Known(t *testing.T) {
	cases := []struct {
		in   string
		want Framework
	}{
		{"Dockerfile", FrameworkDocker},
		{"dockerfile", FrameworkDocker},
		{"DOCKERFILE", FrameworkDocker},
		{"package.json", FrameworkNode},
		{"PACKAGE.JSON", FrameworkNode},
		{"requirements.txt", FrameworkPython},
		{"pyproject.toml", FrameworkPython},
		{"Pipfile", FrameworkPython},
		{"setup.py", FrameworkPython},
		{"go.mod", FrameworkGo},
		{"handler.js", FrameworkUnknown},
		{"README.md", FrameworkUnknown},
		{"index.js", FrameworkUnknown},
		{"", FrameworkUnknown},
	}
	for _, tc := range cases {
		if got := MarkerFor(tc.in); got != tc.want {
			t.Errorf("MarkerFor(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestIsAppMarker(t *testing.T) {
	if !IsAppMarker("package.json") {
		t.Error("IsAppMarker(package.json) = false, want true")
	}
	if IsAppMarker("handler.js") {
		t.Error("IsAppMarker(handler.js) = true, want false")
	}
	if IsAppMarker("README.md") {
		t.Error("IsAppMarker(README.md) = true, want false")
	}
}

func TestMarkers_PriorityOrder(t *testing.T) {
	// The slice order IS the priority order. Dockerfile first so
	// it beats package.json / go.mod when both are root-level.
	got := Markers()
	want := []string{
		"Dockerfile",
		"package.json",
		"requirements.txt",
		"pyproject.toml",
		"Pipfile",
		"setup.py",
		"go.mod",
	}
	if len(got) != len(want) {
		t.Fatalf("Markers() returned %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Markers()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestDetectFromFS_FixtureTableExhaustive exercises every
// marker permutation once. A regression in the priority slice
// (Docker dropped from the front, for example) would flip one
// of these. Companion to TestDetectCLIParity.
func TestDetectFromFS_FixtureTableExhaustive(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  Framework
	}{
		{"dockerfile_alone", []string{"Dockerfile"}, FrameworkDocker},
		{"dockerfile_beats_node", []string{"Dockerfile", "package.json"}, FrameworkDocker},
		{"dockerfile_beats_go", []string{"Dockerfile", "go.mod"}, FrameworkDocker},
		{"node_alone", []string{"package.json"}, FrameworkNode},
		{"node_beats_python", []string{"package.json", "requirements.txt"}, FrameworkNode},
		{"node_beats_go", []string{"package.json", "go.mod"}, FrameworkNode},
		{"python_requirements", []string{"requirements.txt"}, FrameworkPython},
		{"python_pyproject", []string{"pyproject.toml"}, FrameworkPython},
		{"python_pipfile", []string{"Pipfile"}, FrameworkPython},
		{"python_setup", []string{"setup.py"}, FrameworkPython},
		{"python_beats_go", []string{"go.mod", "requirements.txt"}, FrameworkPython},
		{"go_alone", []string{"go.mod"}, FrameworkGo},
		{"empty", nil, FrameworkUnknown},
		{"readme_only", []string{"README.md"}, FrameworkUnknown},
		{"handler_only", []string{"handler.js"}, FrameworkUnknown},
		{"nested_only", []string{"apps/web/package.json"}, FrameworkUnknown},
		{"all_python_markers", []string{"requirements.txt", "pyproject.toml", "Pipfile", "setup.py"}, FrameworkPython},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := seedDirFS(t, tc.files)
			got, err := DetectFromFS(fsys)
			if err != nil {
				t.Fatalf("DetectFromFS: %v", err)
			}
			if got != tc.want {
				t.Errorf("DetectFromFS = %s, want %s", got, tc.want)
			}
		})
	}
}
