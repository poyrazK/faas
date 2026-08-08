package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	crypto_rand "crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeFile is a tiny helper: create parent dirs + write content.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// writeFileBytes writes raw bytes to a path (helper for size-cap tests that
// need to materialise files of arbitrary size).
func writeFileBytes(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// tarEntries reads a gzipped tarball and returns the set of entry names
// (slash-separated, directories keep their trailing slash). Uses os.ReadFile
// (not os.Open) to stay clear of the cmd/gregale forbidigo rule.
func tarEntries(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tarball: %v", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	out := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		out[hdr.Name] = true
	}
	return out
}

func TestDetectFramework(t *testing.T) {
	cases := []struct {
		name  string
		files []string // relative paths to seed (empty file each)
		want  framework
	}{
		{"node", []string{"package.json", "index.js"}, fwNode},
		{"python_requirements", []string{"requirements.txt"}, fwPython},
		{"python_pyproject", []string{"pyproject.toml"}, fwPython},
		{"python_pipfile", []string{"Pipfile"}, fwPython},
		{"python_setup", []string{"setup.py"}, fwPython},
		{"go", []string{"go.mod", "main.go"}, fwGo},
		{"dockerfile_wins_over_node", []string{"Dockerfile", "package.json"}, fwDocker},
		{"dockerfile_case_insensitive", []string{"dockerfile"}, fwDocker},
		{"empty", nil, fwUnknown},
		{"unrelated_only", []string{"README.md", "notes.txt"}, fwUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tc.files {
				writeFile(t, dir, f, "")
			}
			if got := detectFramework(dir); got != tc.want {
				t.Errorf("detectFramework = %q, want %q", got, tc.want)
			}
		})
	}
}

// A go.mod buried in a subdirectory must NOT be detected — the rule is
// top-level-only, matching pkg/builderd/detect.go.
func TestDetectFramework_NestedMarkerIgnored(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "svc/go.mod", "module x")
	writeFile(t, dir, "README.md", "hi")
	if got := detectFramework(dir); got != fwUnknown {
		t.Errorf("detectFramework = %q, want %q (nested go.mod must not count)", got, fwUnknown)
	}
}

// TestDetectNestedMarkerHint pins the depth-2 workspace hint for issue #744
// / ADR-086. The load-bearing case: a monorepo with apps/web/package.json
// (and nothing at the root) returns true so resolveDeployShape can emit the
// `gregale scan --path .` hint. Excluded dirs and depth-3+ must return false
// to avoid false positives.
func TestDetectNestedMarkerHint(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  bool
	}{
		// The headline case — common monorepo layout.
		{"apps_web_package_json", []string{"apps/web/package.json"}, true},
		{"services_api_go_mod", []string{"services/api/go.mod"}, true},
		{"two_nested_markers", []string{"apps/web/package.json", "services/api/go.mod"}, true},
		{"nested_requirements", []string{"api/requirements.txt"}, true},
		{"nested_dockerfile", []string{"deploy/Dockerfile"}, true},
		{"nested_pyproject", []string{"libs/x/pyproject.toml"}, true},

		// Root markers win — these are NOT nested, they're app-shaped;
		// detectFramework already returned the answer for them, and the
		// hint must not double-fire.
		{"root_package_json_wins", []string{"package.json"}, false},
		{"root_dockerfile_wins", []string{"Dockerfile"}, false},

		// Excluded dirs must not false-positive.
		{"node_modules_ignored", []string{"node_modules/x/package.json"}, false},
		{"dot_git_ignored", []string{".git/HEAD"}, false},
		{"vendor_ignored", []string{"vendor/x/go.mod"}, false},
		{"__pycache___ignored", []string{"__pycache__/x/requirements.txt"}, false},

		// Depth-3+ is intentionally out of scope — pkg/reposcan handles it.
		{"depth_3_returns_false", []string{"apps/services/api/package.json"}, false},

		// Empty / README-only — nothing to hint at.
		{"empty_dir", nil, false},
		{"readme_only", []string{"README.md"}, false},

		// Nested dir with non-marker files only — not a workspace.
		{"nested_dir_no_markers", []string{"docs/index.md", "apps/web/index.js"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tc.files {
				writeFile(t, dir, f, "")
			}
			got := detectNestedMarkerHint(dir)
			if got != tc.want {
				t.Errorf("detectNestedMarkerHint = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDetectShape covers issue #737 / ADR-083. The shape detector decides
// whether `gregale deploy` on the cwd auto-picks app mode (any app marker)
// or function mode (single handler.* with no app markers). Cases below
// enumerate every shape boundary the CLI cares about.
func TestDetectShape(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  shape
	}{
		// Function mode (the load-bearing new behaviour).
		{"handler_js_only", []string{"handler.js"}, shapeFunction},
		{"handler_ts_only", []string{"handler.ts"}, shapeFunction},
		{"handler_py_only", []string{"handler.py"}, shapeFunction},
		{"handler_go_only", []string{"handler.go"}, shapeFunction},
		{"handler_with_readme", []string{"handler.js", "README.md"}, shapeFunction},
		{"handler_with_dotfiles", []string{"handler.js", ".env", ".npmrc"}, shapeFunction},

		// App mode — any app marker wins.
		{"package_json_alone", []string{"package.json"}, shapeApp},
		{"requirements_alone", []string{"requirements.txt"}, shapeApp},
		{"go_mod_alone", []string{"go.mod"}, shapeApp},
		{"dockerfile_alone", []string{"Dockerfile"}, shapeApp},
		{"handler_plus_package_json", []string{"handler.js", "package.json"}, shapeApp},
		{"handler_plus_dockerfile", []string{"handler.go", "Dockerfile"}, shapeApp},
		{"two_handlers_is_ambiguous", []string{"handler.js", "handler.py"}, shapeApp},

		// Unknown — the no-source error path.
		{"empty", nil, shapeUnknown},
		{"readme_only", []string{"README.md"}, shapeUnknown},
		{"notes_only", []string{"notes.txt"}, shapeUnknown},
		{"missing_dir_is_unknown", nil, shapeUnknown}, // caller passes non-existent path → ReadDir errors

		// Issue #737 / ADR-083 / macOS APFS (case-insensitive by
		// default): capital-H handler files must still resolve to
		// shapeFunction. Pinned against the regression where
		// functionHandlerFiles lookup was case-sensitive while the
		// app-marker switch used strings.ToLower — silent shapeUnknown
		// on a project that would have deployed end-to-end.
		{"handler_capital_JS", []string{"Handler.JS"}, shapeFunction},
		{"handler_capital_py", []string{"Handler.PY"}, shapeFunction},
		{"handler_mixed_Go", []string{"HANDLER.go"}, shapeFunction},
		{"handler_capital_with_readme", []string{"Handler.js", "README.md"}, shapeFunction},
		// Mixed-case app markers still resolve to app (this
		// already worked; kept here so the case-folding parity
		// between handler and app markers is visible in one place).
		{"package_json_capital_P", []string{"Package.json"}, shapeApp},
		{"dockerfile_capital_D", []string{"DOCKERFILE"}, shapeApp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// For "missing_dir" we use a path that doesn't exist.
			target := dir
			if tc.name == "missing_dir_is_unknown" {
				target = filepath.Join(dir, "does-not-exist")
			}
			for _, f := range tc.files {
				writeFile(t, dir, f, "")
			}
			if got := detectShape(target); got != tc.want {
				t.Errorf("detectShape = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestDetectShape_NestedHandlerIgnored mirrors
// TestDetectFramework_NestedMarkerIgnored — the shape rule is top-level
// only, matching detectFramework's rule. A handler.js in a subdirectory
// does NOT signal function mode (the customer's repo might have a sample
// handler in examples/, that doesn't mean they want function mode).
func TestDetectShape_NestedHandlerIgnored(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "examples/handler.js", "// sample")
	writeFile(t, dir, "README.md", "hi")
	if got := detectShape(dir); got != shapeUnknown {
		t.Errorf("detectShape = %d, want %d (nested handler.js must not count)", got, shapeUnknown)
	}
}

// TestInferFunctionRuntime pins the runtime map from extension (issue #737
// / ADR-083). The wire handler value is always "handler.handler" —
// that's the literal imaged's function-layer manifest rewrites to
// /app/<runtime>.{js,py}, matching the function-* template convention
// (cmd/gregale/templates/function-node/handler.js).
func TestInferFunctionRuntime(t *testing.T) {
	cases := []struct {
		name    string
		files   []string
		wantRt  string
		wantHnd string
		wantOK  bool
	}{
		{"handler_js", []string{"handler.js"}, "node22", "handler.handler", true},
		{"handler_ts", []string{"handler.ts"}, "node22", "handler.handler", true},
		{"handler_py", []string{"handler.py"}, "python312", "handler.handler", true},
		{"handler_go", []string{"handler.go"}, "go124", "handler.handler", true},
		{"handler_with_readme", []string{"handler.js", "README.md"}, "node22", "handler.handler", true},
		{"no_handler", []string{"README.md"}, "", "", false},
		{"two_handlers_ambiguous", []string{"handler.js", "handler.py"}, "", "", false},
		{"app_marker_present_ignored", []string{"handler.js", "package.json"}, "node22", "handler.handler", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tc.files {
				writeFile(t, dir, f, "")
			}
			rt, hnd, ok := inferFunctionRuntime(dir)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if rt != tc.wantRt {
				t.Errorf("runtime = %q, want %q", rt, tc.wantRt)
			}
			if hnd != tc.wantHnd {
				t.Errorf("handler = %q, want %q", hnd, tc.wantHnd)
			}
		})
	}
}

func TestPackDirToTarGz_TopLevelDirAndCount(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Base(dir)
	writeFile(t, dir, "package.json", "{}")
	writeFile(t, dir, "src/index.js", "console.log(1)")

	dest := filepath.Join(t.TempDir(), "out.tar.gz")
	n, err := packDirToTarGz(dir, dest)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if n != 2 {
		t.Errorf("fileCount = %d, want 2", n)
	}
	got := tarEntries(t, dest)
	for _, want := range []string{base + "/package.json", base + "/src/index.js"} {
		if !got[want] {
			t.Errorf("archive missing %q; entries: %v", want, got)
		}
	}
}

func TestPackDirToTarGz_Excludes(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Base(dir)
	// Kept:
	writeFile(t, dir, "package.json", "{}")
	writeFile(t, dir, ".env", "SECRET=1") // dotfile kept on purpose
	writeFile(t, dir, ".dockerignore", "node_modules")
	// Dropped:
	writeFile(t, dir, ".git/HEAD", "ref: refs/heads/main")
	writeFile(t, dir, "node_modules/left-pad/index.js", "module.exports=1")
	writeFile(t, dir, "vendor/x/x.go", "package x")
	writeFile(t, dir, "app/__pycache__/mod.cpython-312.pyc", "bytecode")
	writeFile(t, dir, "app/mod.pyc", "bytecode")
	writeFile(t, dir, ".DS_Store", "junk")

	dest := filepath.Join(t.TempDir(), "out.tar.gz")
	n, err := packDirToTarGz(dir, dest)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	got := tarEntries(t, dest)

	mustHave := []string{base + "/package.json", base + "/.env", base + "/.dockerignore"}
	for _, w := range mustHave {
		if !got[w] {
			t.Errorf("archive should contain %q; entries: %v", w, got)
		}
	}
	// n counts regular files: package.json, .env, .dockerignore = 3.
	if n != 3 {
		t.Errorf("fileCount = %d, want 3 (only kept files); entries: %v", n, got)
	}
	for name := range got {
		for _, bad := range []string{"/.git/", "/node_modules/", "/vendor/", "/__pycache__/", ".pyc", ".DS_Store"} {
			if strings.Contains(name, bad) {
				t.Errorf("archive should not contain %q (matched %q)", name, bad)
			}
		}
	}
}

func TestPackDirToTarGz_RejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink not supported on Windows")
	}
	dir := t.TempDir()
	writeFile(t, dir, "real.txt", "hi")
	if err := os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "out.tar.gz")
	if _, err := packDirToTarGz(dir, dest); err == nil {
		t.Fatal("packDirToTarGz should reject a symlink, got nil error")
	}
}

func TestPackDirToTarGz_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(t.TempDir(), "out.tar.gz")
	n, err := packDirToTarGz(dir, dest)
	if err != nil {
		t.Fatalf("pack empty: %v", err)
	}
	if n != 0 {
		t.Errorf("fileCount = %d, want 0", n)
	}
	// Archive must still be a valid, readable gzip tar (possibly zero entries).
	_ = tarEntries(t, dest)
}

// TestPackDirToTarGz_TotalSizeCap pins the zero-config zeroConfigSourceCapMB
// preflight: a cwd that packs to > cap must surface a friendly error before
// any HTTP round-trip. Uses many small files so per-file is not the trigger.
// Bytes are crypto/random so gzip can't compress them away.
func TestPackDirToTarGz_TotalSizeCap(t *testing.T) {
	if testing.Short() {
		t.Skip("size-cap test materialises > 100 MB; skip in -short mode")
	}
	dir := t.TempDir()
	const oneMiB = 1024 * 1024
	// 110 × 1 MiB of crypto-random bytes (110 MiB raw, ~110 MiB after gzip).
	// Each file is well under the per-file cap (100 MiB), so the total-cap
	// stat check is what trips.
	const totalFiles = zeroConfigSourceCapMB + 10
	for i := 0; i < totalFiles; i++ {
		chunk := make([]byte, oneMiB)
		if _, err := io.ReadFull(crypto_rand.Reader, chunk); err != nil {
			t.Fatalf("rand: %v", err)
		}
		writeFileBytes(t, filepath.Join(dir, fmt.Sprintf("chunk-%04d.bin", i)), chunk)
	}

	dest := filepath.Join(t.TempDir(), "out.tar.gz")
	_, err := packDirToTarGz(dir, dest)
	if err == nil {
		t.Fatal("packDirToTarGz should reject total size > cap, got nil error")
	}
	if !strings.Contains(err.Error(), "zero-config cap") {
		t.Errorf("expected friendly total-cap error; got %v", err)
	}
}

// TestPackDirToTarGz_PerFileCap pins the per-file guard inside copyRegular: a
// single file >= cap must be rejected while still being streamed, instead of
// materialising the whole thing into the gzip writer first.
func TestPackDirToTarGz_PerFileCap(t *testing.T) {
	if testing.Short() {
		t.Skip("per-file cap test materialises a > cap file")
	}
	dir := t.TempDir()
	huge := make([]byte, zeroConfigSourceCapMB*1024*1024)
	if _, err := io.ReadFull(crypto_rand.Reader, huge); err != nil {
		t.Fatalf("rand: %v", err)
	}
	writeFileBytes(t, filepath.Join(dir, "blob.bin"), huge)

	dest := filepath.Join(t.TempDir(), "out.tar.gz")
	_, err := packDirToTarGz(dir, dest)
	if err == nil {
		t.Fatal("packDirToTarGz should reject a single file >= per-file cap, got nil")
	}
	if !strings.Contains(err.Error(), "per-file cap") {
		t.Errorf("expected per-file cap error; got %v", err)
	}
}

// TestPackDirToTarGz_JustUnderTotalCap passes when the packed tarball sits
// predictably under cap — pins that the preflight doesn't false-positive.
// ~50 MiB raw (compressible to a fraction under cap).
func TestPackDirToTarGz_JustUnderTotalCap(t *testing.T) {
	if testing.Short() {
		t.Skip("size-cap test materialises near-cap; skip in -short mode")
	}
	dir := t.TempDir()
	// 50 MiB raw, written as one file under the per-file cap. Far enough
	// from zeroConfigSourceCapMB (100 MB) that gzip compression won't
	// matter — even if it expands slightly the tarball stays well under
	// cap.
	const fiftyMiB = 50 * 1024 * 1024
	chunk := make([]byte, fiftyMiB)
	if _, err := io.ReadFull(crypto_rand.Reader, chunk); err != nil {
		t.Fatalf("rand: %v", err)
	}
	writeFileBytes(t, filepath.Join(dir, "well_under.bin"), chunk)

	dest := filepath.Join(t.TempDir(), "out.tar.gz")
	if _, err := packDirToTarGz(dir, dest); err != nil {
		t.Fatalf("packDirToTarGz well under cap, want pass; got %v", err)
	}
}

func TestAutoPackCwd(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", "{}")
	writeFile(t, dir, "index.js", "x")

	path, fw, n, err := autoPackCwd(dir)
	if err != nil {
		t.Fatalf("autoPackCwd: %v", err)
	}
	defer func() { _ = os.Remove(path) }()
	if fw != fwNode {
		t.Errorf("framework = %q, want %q", fw, fwNode)
	}
	if n != 2 {
		t.Errorf("fileCount = %d, want 2", n)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected tarball at %s: %v", path, err)
	}
	_ = tarEntries(t, path) // must be a valid gzipped tar
}

// On a pack error autoPackCwd must not leave its temp file behind.
func TestAutoPackCwd_CleansUpOnError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink not supported on Windows")
	}
	dir := t.TempDir()
	writeFile(t, dir, "real.txt", "hi")
	if err := os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	path, _, _, err := autoPackCwd(dir)
	if err == nil {
		t.Fatal("expected error from autoPackCwd on symlink dir")
	}
	if path != "" {
		t.Errorf("path = %q, want empty on error", path)
	}
}

// TestDetectFrameworkVersion_NodeNvmrc pins the CLI-side mirror of
// pkg/builderd/detectversion.go (issue #740 / DEPLOY-PROV-5 /
// ADR-087). The CLI banner must read .nvmrc when present and surface
// the bare version.
func TestDetectFrameworkVersion_NodeNvmrc(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".nvmrc", "22.11.0")
	writeFile(t, dir, "package.json", "{}")
	got := detectFrameworkVersion(dir, fwNode)
	if got != "22.11.0" {
		t.Errorf("got %q, want %q", got, "22.11.0")
	}
}

// TestDetectFrameworkVersion_NodeNvmrcVPrefix pins the leading-v
// strip (".nvmrc" commonly writes "v22.11.0").
func TestDetectFrameworkVersion_NodeNvmrcVPrefix(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".nvmrc", "v22.11.0")
	writeFile(t, dir, "package.json", "{}")
	got := detectFrameworkVersion(dir, fwNode)
	if got != "22.11.0" {
		t.Errorf("got %q, want %q", got, "22.11.0")
	}
}

// TestDetectFrameworkVersion_NodeEngines pins the package.json
// fallback path. The caret-prefix is stripped; only the bare version
// is returned.
func TestDetectFrameworkVersion_NodeEngines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"engines":{"node":"^22.11.0"}}`)
	got := detectFrameworkVersion(dir, fwNode)
	if got != "22.11.0" {
		t.Errorf("got %q, want %q", got, "22.11.0")
	}
}

// TestDetectFrameworkVersion_NvmrcWinsOverEngines pins the priority
// order. .nvmrc takes precedence over engines.node when both are
// present.
func TestDetectFrameworkVersion_NvmrcWinsOverEngines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".nvmrc", "20.10.0")
	writeFile(t, dir, "package.json", `{"engines":{"node":">=22.11.0"}}`)
	got := detectFrameworkVersion(dir, fwNode)
	if got != "20.10.0" {
		t.Errorf("got %q, want %q (.nvmrc wins)", got, "20.10.0")
	}
}

// TestDetectFrameworkVersion_PythonPythonVersion pins the
// python-version path.
func TestDetectFrameworkVersion_PythonPythonVersion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".python-version", "3.11.0")
	got := detectFrameworkVersion(dir, fwPython)
	if got != "3.11.0" {
		t.Errorf("got %q, want %q", got, "3.11.0")
	}
}

// TestDetectFrameworkVersion_PythonRequiresPython pins the
// pyproject.toml requires-python path.
func TestDetectFrameworkVersion_PythonRequiresPython(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", `[project]
name = "demo"
requires-python = ">=3.13"
`)
	got := detectFrameworkVersion(dir, fwPython)
	if got != "3.13" {
		t.Errorf("got %q, want %q", got, "3.13")
	}
}

// TestDetectFrameworkVersion_GoDirective pins the go.mod directive
// path.
func TestDetectFrameworkVersion_GoDirective(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", `module example.com/foo

go 1.24
`)
	got := detectFrameworkVersion(dir, fwGo)
	if got != "1.24" {
		t.Errorf("got %q, want %q", got, "1.24")
	}
}

// TestDetectFrameworkVersion_EmptyOnUnknown pins the negative case:
// no version file → "". Customer just sees the framework= banner.
func TestDetectFrameworkVersion_EmptyOnUnknown(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", "{}")
	got := detectFrameworkVersion(dir, fwNode)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// TestDetectFrameworkVersion_EmptyOnMalformedJSON pins the
// best-effort parsing contract: a malformed package.json must NOT
// panic or error; the function returns "" and the banner omits the
// version= token.
func TestDetectFrameworkVersion_EmptyOnMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{"engines": { "node": `)
	got := detectFrameworkVersion(dir, fwNode)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// TestDetectFrameworkVersion_DockerEmpty pins the explicit
// out-of-scope: Docker mode never returns a version (FROM parsing is
// intentionally not implemented per issue #740).
func TestDetectFrameworkVersion_DockerEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Dockerfile", "FROM scratch")
	got := detectFrameworkVersion(dir, fwDocker)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// TestDetectFrameworkVersion_NoOpOnUnknownFramework pins the
// unknown-framework branch: returns "" without reading the cwd.
func TestDetectFrameworkVersion_NoOpOnUnknownFramework(t *testing.T) {
	dir := t.TempDir()
	got := detectFrameworkVersion(dir, fwUnknown)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// TestRuntimeSuggestionFor pins the (framework, version) →
// whitelisted runtime name mapping used by the function error path's
// runtime suggestion (issue #740 / ADR-087). The mapping is the
// single source of truth for which `--runtime` value the CLI prints
// in the error message; pinning here means a new runtime addition
// forces an explicit test change.
func TestRuntimeSuggestionFor(t *testing.T) {
	cases := []struct {
		name string
		fw   framework
		ver  string
		want string
	}{
		{"node_22", fwNode, "22.11.0", "node22"},
		{"node_24", fwNode, "24.0.0", "node24"},
		{"node_20_falls_back_to_node22", fwNode, "20.0.0", "node22"},
		{"node_18_older_falls_back_to_node22", fwNode, "18.0.0", "node22"},
		{"python_313", fwPython, "3.13.0", "python313"},
		{"python_312", fwPython, "3.12.0", "python312"},
		{"python_311_falls_back_to_python312", fwPython, "3.11.0", "python312"},
		{"python_310_older_falls_back_to_python312", fwPython, "3.10.0", "python312"},
		{"go_124", fwGo, "1.24.0", "go124"},
		{"go_122_below_whitelist", fwGo, "1.22.0", ""},
		{"docker_no_op", fwDocker, "1.0", ""},
		{"empty_version", fwNode, "", ""},
		{"malformed_version", fwNode, "not-a-version", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runtimeSuggestionFor(tc.fw, tc.ver)
			if got != tc.want {
				t.Errorf("runtimeSuggestionFor(%q, %q) = %q, want %q", tc.fw, tc.ver, got, tc.want)
			}
		})
	}
}
