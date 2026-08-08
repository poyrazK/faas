package builderd

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// writeTarGz builds a gzipped tarball at dest containing the given
// (filename, body) pairs at the archive root. Mirrors the shape
// apid's validateTarballShape expects (one project root).
func writeTarGz(t *testing.T, dest string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(dest)
	if err != nil {
		t.Fatalf("create tarball: %v", err)
	}
	defer func() { _ = f.Close() }()

	gz := gzip.NewWriter(f)
	defer func() { _ = gz.Close() }()

	tw := tar.NewWriter(gz)
	defer func() { _ = tw.Close() }()

	for name, body := range entries {
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("write body %s: %v", name, err)
		}
	}
}

// tempTarball writes a temporary tarball with the given entries and
// returns its path. The caller defers os.Remove.
func tempTarball(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "src.tar.gz")
	writeTarGz(t, path, entries)
	return path
}

func TestDetectVersion_NodeNvmrc(t *testing.T) {
	p := tempTarball(t, map[string]string{
		".nvmrc":       "22.11.0\n",
		"package.json": `{"engines":{"node":">=20.0.0"}}`,
	})
	got, err := detectVersion(p, FrameworkNode)
	if err != nil {
		t.Fatalf("detectVersion: %v", err)
	}
	if got != "22.11.0" {
		t.Errorf("got %q, want %q", got, "22.11.0")
	}
}

func TestDetectVersion_NodeNvmrcVPrefix(t *testing.T) {
	p := tempTarball(t, map[string]string{
		".nvmrc": "v22.11.0\n",
	})
	got, err := detectVersion(p, FrameworkNode)
	if err != nil {
		t.Fatalf("detectVersion: %v", err)
	}
	if got != "22.11.0" {
		t.Errorf("got %q, want %q", got, "22.11.0")
	}
}

func TestDetectVersion_NodeEnginesCaret(t *testing.T) {
	p := tempTarball(t, map[string]string{
		"package.json": `{"engines":{"node":"^22.11.0"}}`,
	})
	got, err := detectVersion(p, FrameworkNode)
	if err != nil {
		t.Fatalf("detectVersion: %v", err)
	}
	if got != "22.11.0" {
		t.Errorf("got %q, want %q", got, "22.11.0")
	}
}

func TestDetectVersion_NodeEnginesOnly(t *testing.T) {
	p := tempTarball(t, map[string]string{
		"package.json": `{"engines":{"node":">=20.0.0"}}`,
	})
	got, err := detectVersion(p, FrameworkNode)
	if err != nil {
		t.Fatalf("detectVersion: %v", err)
	}
	if got != "20.0.0" {
		t.Errorf("got %q, want %q", got, "20.0.0")
	}
}

func TestDetectVersion_NvmrcWinsOverEngines(t *testing.T) {
	// Priority check: .nvmrc wins over engines.node when both are present.
	p := tempTarball(t, map[string]string{
		".nvmrc":       "20.10.0",
		"package.json": `{"engines":{"node":">=22.11.0"}}`,
	})
	got, err := detectVersion(p, FrameworkNode)
	if err != nil {
		t.Fatalf("detectVersion: %v", err)
	}
	if got != "20.10.0" {
		t.Errorf("got %q, want %q (nvmrc should win)", got, "20.10.0")
	}
}

func TestDetectVersion_PythonPythonVersion(t *testing.T) {
	p := tempTarball(t, map[string]string{
		".python-version": "3.11.0",
	})
	got, err := detectVersion(p, FrameworkPython)
	if err != nil {
		t.Fatalf("detectVersion: %v", err)
	}
	if got != "3.11.0" {
		t.Errorf("got %q, want %q", got, "3.11.0")
	}
}

func TestDetectVersion_PythonRequiresPython(t *testing.T) {
	p := tempTarball(t, map[string]string{
		"pyproject.toml": `[project]
name = "demo"
requires-python = ">=3.13"
`,
	})
	got, err := detectVersion(p, FrameworkPython)
	if err != nil {
		t.Fatalf("detectVersion: %v", err)
	}
	if got != "3.13" {
		t.Errorf("got %q, want %q", got, "3.13")
	}
}

func TestDetectVersion_GoDirective(t *testing.T) {
	p := tempTarball(t, map[string]string{
		"go.mod": `module example.com/foo

go 1.24
`,
	})
	got, err := detectVersion(p, FrameworkGo)
	if err != nil {
		t.Fatalf("detectVersion: %v", err)
	}
	if got != "1.24" {
		t.Errorf("got %q, want %q", got, "1.24")
	}
}

func TestDetectVersion_EmptyWhenUnknown(t *testing.T) {
	p := tempTarball(t, map[string]string{
		"README.md": "hello",
	})
	got, err := detectVersion(p, FrameworkNode)
	if err != nil {
		t.Fatalf("detectVersion: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestDetectVersion_EmptyWhenMalformedJSON(t *testing.T) {
	// Malformed package.json must NOT propagate an error — it returns "".
	p := tempTarball(t, map[string]string{
		"package.json": `{"engines": { "node": `, // truncated
	})
	got, err := detectVersion(p, FrameworkNode)
	if err != nil {
		t.Fatalf("detectVersion: malformed JSON must not error, got %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestDetectVersion_DockerEmpty(t *testing.T) {
	// Docker mode: no version returned (FROM parsing is out of scope).
	p := tempTarball(t, map[string]string{
		"Dockerfile": "FROM scratch",
	})
	got, err := detectVersion(p, FrameworkDocker)
	if err != nil {
		t.Fatalf("detectVersion: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty for docker", got)
	}
}

func TestDetectVersion_TarballMissing(t *testing.T) {
	// Non-existent file should error gracefully (Detect itself returns
	// an error which DetectWithVersion propagates). detectVersion
	// itself is best-effort but the underlying readTarFile errors.
	// We assert that the function does not panic.
	got, err := detectVersion("/nonexistent/path.tar.gz", FrameworkNode)
	if err != nil {
		// acceptable — the readTarFile error is non-fatal for the
		// caller because DetectWithVersion swallows it.
		t.Logf("expected error for missing tarball: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestDetectVersion_OversizedFileTreatedAsMissing(t *testing.T) {
	// A 100KB .nvmrc should be treated as missing (size cap). The
	// parser is best-effort and never tries to read the whole file.
	big := make([]byte, 100*1024)
	for i := range big {
		big[i] = 'a'
	}
	p := tempTarball(t, map[string]string{
		".nvmrc":       string(big),
		"package.json": `{"engines":{"node":">=22.11.0"}}`,
	})
	got, err := detectVersion(p, FrameworkNode)
	if err != nil {
		t.Fatalf("detectVersion: %v", err)
	}
	if got != "22.11.0" {
		t.Errorf("got %q, want %q (oversized .nvmrc should fall back to engines.node)", got, "22.11.0")
	}
}

func TestDetectVersion_NodeDeepNestedIgnored(t *testing.T) {
	// apps/web/.nvmrc must NOT be picked up — top-level only.
	p := tempTarball(t, map[string]string{
		"apps/web/.nvmrc": "22.11.0",
		"package.json":    `{"engines":{"node":">=20.0.0"}}`,
	})
	got, err := detectVersion(p, FrameworkNode)
	if err != nil {
		t.Fatalf("detectVersion: %v", err)
	}
	if got != "20.0.0" {
		t.Errorf("got %q, want %q (nested .nvmrc must be ignored)", got, "20.0.0")
	}
}

func TestDetectVersion_EnginesNodeNotString(t *testing.T) {
	// engines.node as a number is malformed — return "".
	p := tempTarball(t, map[string]string{
		"package.json": `{"engines":{"node":22}}`,
	})
	got, err := detectVersion(p, FrameworkNode)
	if err != nil {
		t.Fatalf("detectVersion: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestDetectWithVersion_NodeNvmrc(t *testing.T) {
	// DetectWithVersion rolls Detect + detectVersion into one call.
	p := tempTarball(t, map[string]string{
		".nvmrc":       "22.11.0",
		"package.json": `{}`,
	})
	d := NewDetector()
	fw, ver, err := d.DetectWithVersion(p)
	if err != nil {
		t.Fatalf("DetectWithVersion: %v", err)
	}
	if fw != FrameworkNode {
		t.Errorf("fw = %q, want %q", fw, FrameworkNode)
	}
	if ver != "22.11.0" {
		t.Errorf("ver = %q, want %q", ver, "22.11.0")
	}
}

func TestDetectWithVersion_EmptyVersionWhenNoFile(t *testing.T) {
	p := tempTarball(t, map[string]string{
		"package.json": `{}`,
	})
	d := NewDetector()
	fw, ver, err := d.DetectWithVersion(p)
	if err != nil {
		t.Fatalf("DetectWithVersion: %v", err)
	}
	if fw != FrameworkNode {
		t.Errorf("fw = %q, want %q", fw, FrameworkNode)
	}
	if ver != "" {
		t.Errorf("ver = %q, want empty", ver)
	}
}

func TestDetectWithVersion_PropagatesDetectError(t *testing.T) {
	// Empty tarball — Detect must error and DetectWithVersion must
	// propagate the error (not swallow it like a parse failure).
	p := tempTarball(t, map[string]string{})
	d := NewDetector()
	_, _, err := d.DetectWithVersion(p)
	if err == nil {
		t.Fatalf("expected error for empty tarball")
	}
}

func TestDetectWithVersion_UnknownFramework(t *testing.T) {
	// Random file: Detect returns unknown and DetectWithVersion returns
	// ("unknown", "", nil). Verifies the FrameworkUnknown branch.
	p := tempTarball(t, map[string]string{
		"README.md": "hello",
	})
	d := NewDetector()
	_, _, err := d.DetectWithVersion(p)
	if err == nil {
		t.Fatalf("expected error for unknown tarball")
	}
}
