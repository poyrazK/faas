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

// tarEntryBody returns the bytes of a single archive entry, matching by
// suffix so callers can pass either the full "root/.env.production" or
// the bare ".env.production" basename. Returns nil if no entry matches.
func tarEntryBody(t *testing.T, path, suffix string) []byte {
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
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		if hdr.Name == suffix || strings.HasSuffix(hdr.Name, "/"+suffix) {
			body, rerr := io.ReadAll(tr)
			if rerr != nil {
				t.Fatalf("read body %s: %v", hdr.Name, rerr)
			}
			return body
		}
	}
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

		// Depth-3 now visible (issue #1182 §P1 follow-up). Depth-4+
		// remains out of scope — pkg/reposcan handles it.
		{"depth_3_returns_true", []string{"apps/services/api/package.json"}, true},
		{"depth_4_still_out_of_scope", []string{"apps/web/services/api/package.json"}, false},

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
	n, err := packDirToTarGz(dir, dest, defaultZeroConfigSourceCapMB, nil)
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
	n, err := packDirToTarGz(dir, dest, defaultZeroConfigSourceCapMB, nil)
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

func TestPackDirToTarGz_ExcludesLinkedWorktreeGitFile(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Base(dir)
	writeFile(t, dir, ".git", "gitdir: /tmp/linked-worktree\n")
	writeFile(t, dir, "package.json", "{}")

	dest := filepath.Join(t.TempDir(), "out.tar.gz")
	n, err := packDirToTarGz(dir, dest, defaultZeroConfigSourceCapMB, nil)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if n != 1 {
		t.Errorf("fileCount = %d, want 1", n)
	}
	got := tarEntries(t, dest)
	if got[base+"/.git"] {
		t.Errorf("archive contains linked-worktree .git file")
	}
	if !got[base+"/package.json"] {
		t.Errorf("archive missing package.json")
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
	if _, err := packDirToTarGz(dir, dest, defaultZeroConfigSourceCapMB, nil); err == nil {
		t.Fatal("packDirToTarGz should reject a symlink, got nil error")
	}
}

func TestPackDirToTarGz_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(t.TempDir(), "out.tar.gz")
	n, err := packDirToTarGz(dir, dest, defaultZeroConfigSourceCapMB, nil)
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
	const totalFiles = defaultZeroConfigSourceCapMB + 10
	for i := 0; i < totalFiles; i++ {
		chunk := make([]byte, oneMiB)
		if _, err := io.ReadFull(crypto_rand.Reader, chunk); err != nil {
			t.Fatalf("rand: %v", err)
		}
		writeFileBytes(t, filepath.Join(dir, fmt.Sprintf("chunk-%04d.bin", i)), chunk)
	}

	dest := filepath.Join(t.TempDir(), "out.tar.gz")
	_, err := packDirToTarGz(dir, dest, defaultZeroConfigSourceCapMB, nil)
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
	// Strictly larger than cap so the LimitReader guard trips. Prior to
	// the LimitReader fix in copyRegular the test materialised exactly
	// cap bytes; the new code allows exactly-at-cap (LimitReader(cap+1)
	// reads at most cap+1, and `n > cap` rejects only strictly larger).
	huge := make([]byte, (defaultZeroConfigSourceCapMB+1)*1024*1024)
	if _, err := io.ReadFull(crypto_rand.Reader, huge); err != nil {
		t.Fatalf("rand: %v", err)
	}
	writeFileBytes(t, filepath.Join(dir, "blob.bin"), huge)

	dest := filepath.Join(t.TempDir(), "out.tar.gz")
	_, err := packDirToTarGz(dir, dest, defaultZeroConfigSourceCapMB, nil)
	if err == nil {
		t.Fatal("packDirToTarGz should reject a single file > per-file cap, got nil")
	}
	if !strings.Contains(err.Error(), "per-file cap") {
		t.Errorf("expected per-file cap error; got %v", err)
	}
}

// TestPackDirToTarGz_PerFileCapExactlyAtCap pins the boundary semantics
// of the LimitReader guard in copyRegular: a file whose size is exactly
// capBytes (post-compression) is ALLOWED, and only strictly-larger files
// are rejected. The pre-fix code used a post-hoc `>= warnBytes` check
// after io.Copy(tw, f) and rejected exactly-at-cap; the LimitReader fix
// (issue #1182) changes that to `> cap` so the cap is now a permissive
// ceiling rather than a hard exclusion.
func TestPackDirToTarGz_PerFileCapExactlyAtCap(t *testing.T) {
	if testing.Short() {
		t.Skip("per-file cap boundary test materialises a cap-sized file")
	}
	dir := t.TempDir()
	atCap := make([]byte, defaultZeroConfigSourceCapMB*1024*1024)
	if _, err := io.ReadFull(crypto_rand.Reader, atCap); err != nil {
		t.Fatalf("rand: %v", err)
	}
	writeFileBytes(t, filepath.Join(dir, "blob.bin"), atCap)

	dest := filepath.Join(t.TempDir(), "out.tar.gz")
	if _, err := packDirToTarGz(dir, dest, defaultZeroConfigSourceCapMB, nil); err != nil {
		// Random bytes don't compress, so the on-disk tarball will be
		// near capBytes and trip the TOTAL cap check downstream even
		// though the per-file LimitReader allowed it. That's the
		// downstream total-cap gate doing its job, not a per-file
		// regression — assert the error is the total cap, not per-file.
		if strings.Contains(err.Error(), "per-file cap") {
			t.Fatalf("exactly-at-cap should not trip the per-file LimitReader; got %v", err)
		}
		if !strings.Contains(err.Error(), "zero-config cap") {
			t.Fatalf("expected total-cap error; got %v", err)
		}
		return
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
	if _, err := packDirToTarGz(dir, dest, defaultZeroConfigSourceCapMB, nil); err != nil {
		t.Fatalf("packDirToTarGz well under cap, want pass; got %v", err)
	}
}

func TestAutoPackCwd(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", "{}")
	writeFile(t, dir, "index.js", "x")

	path, fw, n, err := autoPackCwd(dir, defaultZeroConfigSourceCapMB, nil)
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
	path, _, _, err := autoPackCwd(dir, defaultZeroConfigSourceCapMB, nil)
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

// TestFuncErrorSuggestion pins the high-level "should the error
// message include a marker suggestion, and if so does it have a
// non-empty --runtime" contract. Regression: a previous version of
// the suggestion logic emitted "Detected X project — try `--runtime
//
//	--handler ...`." with an empty --runtime when the version mapped
//
// to no whitelisted runtime. This test pins that the suggestion is
// only emitted when runtimeSuggestionFor returns non-empty.
func TestFuncErrorSuggestion(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		wantSub string // substring the suggestion must contain; "" = no suggestion
	}{
		{
			name:    "no_marker_no_suggestion",
			files:   nil, // empty dir — no package.json, no go.mod
			wantSub: "",
		},
		{
			name:    "node_with_nvmrc_emits_full_suggestion",
			files:   map[string]string{".nvmrc": "22.11.0", "package.json": "{}"},
			wantSub: "`--runtime node22 --handler handler.handler`",
		},
		{
			name:  "node_with_nvmrc_no_empty_runtime_arg",
			files: map[string]string{".nvmrc": "22.11.0", "package.json": "{}"},
			// Sanity: a valid suggestion must never contain
			// `--runtime  --handler` with an empty runtime. This
			// is the regression-pin for the bug where
			// runtimeSuggestionFor returned "" but the suggestion
			// was still emitted with the empty arg.
			wantSub: "`--runtime node22 --handler",
		},
		{
			name:  "go_with_no_whitelisted_runtime_emits_no_suggestion",
			files: map[string]string{"go.mod": "module x\n\ngo 1.22\n"},
			// Even though a version IS detected (1.22),
			// runtimeSuggestionFor("go", "1.22") == "" because
			// the only whitelisted Go runtime is 1.24+, so the
			// suggestion is suppressed rather than emitted with
			// an empty --runtime.
			wantSub: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for k, v := range tc.files {
				writeFile(t, dir, k, v)
			}
			got := funcErrorSuggestion(dir)
			if tc.wantSub == "" {
				if got != "" {
					t.Errorf("expected empty suggestion, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("expected suggestion to contain %q; got %q", tc.wantSub, got)
			}
			// Negative pin: no valid suggestion may contain an
			// empty --runtime. This is the bug we're guarding
			// against regression of.
			if strings.Contains(got, "`--runtime  --handler") {
				t.Errorf("suggestion must not contain empty --runtime; got %q", got)
			}
		})
	}
}

// fakeStripeLiveKey mirrors the var in pkg/secretscan/scan_test.go.
// Declared locally because pack_test.go is `package main` — the scan
// package's test var isn't reachable. The runtime behaviour is identical:
// the regex matches, the Finding.Provider is "stripe_live".
var fakeStripeLiveKey = "sk" + "_" + "live" + "_" + "XXXXXXXXXXXXXXXXXXXXXXXXXXXX"

// TestScanAndRedactEnvFiles_StripsStripeKey pins the most common case:
// a Stripe live key committed to .env.production by accident. The
// override map must contain the redacted bytes; the unredacted key must
// NOT survive in the archive.
//
// The key literal is assembled via concatenation (see fakeStripeLiveKey)
// so GitHub's secret-scanner doesn't flag the literal pattern on push.
// The regex in pkg/secretscan still matches it at runtime.
func TestScanAndRedactEnvFiles_StripsStripeKey(t *testing.T) {
	dir := t.TempDir()
	contents := "PORT=8080\nSTRIPE_SECRET_KEY=" + fakeStripeLiveKey + "\nDATABASE_URL=postgres://u:p@h:5432/d\n"
	writeFile(t, dir, ".env.production", contents)

	overrides, findings, err := scanAndRedactEnvFiles(dir, modeWarn)
	if err != nil {
		t.Fatalf("scanAndRedactEnvFiles: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(findings), findings)
	}
	if findings[0].Provider != "stripe_live" {
		t.Errorf("Provider = %q, want stripe_live", findings[0].Provider)
	}
	if findings[0].Line != 2 {
		t.Errorf("Line = %d, want 2", findings[0].Line)
	}
	got, ok := overrides[".env.production"]
	if !ok {
		t.Fatalf("override missing for .env.production; got keys = %v", keysOf(overrides))
	}
	if strings.Contains(string(got), fakeStripeLiveKey) {
		t.Errorf("override still contains raw stripe key: %q", got)
	}
	if !strings.Contains(string(got), "<REDACTED # secret detected: stripe_live>") {
		t.Errorf("override should contain redacted placeholder, got: %q", got)
	}
	if !strings.Contains(string(got), "PORT=8080") {
		t.Errorf("clean line lost in override: %q", got)
	}
	if !strings.Contains(string(got), "DATABASE_URL=postgres://u:p@h:5432/d") {
		t.Errorf("URL line lost in override (URL carve-out should preserve): %q", got)
	}
}

// TestScanAndRedactEnvFiles_CleanDir_NoOp pins the no-findings fast path.
func TestScanAndRedactEnvFiles_CleanDir_NoOp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "PORT=8080\nGREETING=hello\n")
	writeFile(t, dir, "main.go", "package main\n")

	overrides, findings, err := scanAndRedactEnvFiles(dir, modeWarn)
	if err != nil {
		t.Fatalf("scanAndRedactEnvFiles: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %d, want 0: %+v", len(findings), findings)
	}
	if len(overrides) != 0 {
		t.Errorf("overrides = %v, want empty", overrides)
	}
}

// TestScanAndRedactEnvFiles_Disabled pins the --secret-scan=off escape.
// overrides/findings must both be nil even when the file is poisoned.
func TestScanAndRedactEnvFiles_Disabled(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "STRIPE_SECRET_KEY="+fakeStripeLiveKey+"\n")

	overrides, findings, err := scanAndRedactEnvFiles(dir, modeOff)
	if err != nil {
		t.Fatalf("scanAndRedactEnvFiles(disabled): %v", err)
	}
	if findings != nil {
		t.Errorf("findings should be nil when disabled, got %+v", findings)
	}
	if overrides != nil {
		t.Errorf("overrides should be nil when disabled, got %v", overrides)
	}
}

// TestPackDirToTarGz_WithEnvOverride pins the integration: when the
// override map contains a redacted .env.production, the archive must
// contain the redacted bytes, NOT the original poisoned bytes.
func TestPackDirToTarGz_WithEnvOverride(t *testing.T) {
	dir := t.TempDir()
	original := "PORT=8080\nSTRIPE_SECRET_KEY=" + fakeStripeLiveKey + "\n"
	writeFile(t, dir, ".env.production", original)
	writeFile(t, dir, "main.go", "package main\n")

	overrides, _, err := scanAndRedactEnvFiles(dir, modeWarn)
	if err != nil {
		t.Fatalf("scanAndRedactEnvFiles: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "out.tar.gz")
	if _, err := packDirToTarGz(dir, dest, defaultZeroConfigSourceCapMB, overrides); err != nil {
		t.Fatalf("pack: %v", err)
	}

	entries := tarEntries(t, dest)
	foundEnv := false
	for name := range entries {
		if strings.HasSuffix(name, ".env.production") {
			foundEnv = true
			break
		}
	}
	if !foundEnv {
		t.Fatalf("archive missing .env.production; entries: %v", entries)
	}
	body := tarEntryBody(t, dest, ".env.production")
	if strings.Contains(string(body), fakeStripeLiveKey) {
		t.Errorf("archive still contains raw stripe key: %q", body)
	}
	if !strings.Contains(string(body), "<REDACTED") {
		t.Errorf("archive should contain redacted placeholder, got: %q", body)
	}
}

// keysOf is a small test helper: return map keys as a sorted slice for
// stable error messages. Avoids pulling sort into every assertion.
func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestParseGregaleignore exercises the pure parser against the
// gitignore-subset grammar documented on gregaleignoreFile /
// parseGregaleignore. Pins the four modifier flags and the glob
// handling so a future refactor can't silently change semantics.
func TestParseGregaleignore(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []gregaleignorePattern
	}{
		{
			name:  "empty",
			input: "",
			want:  nil,
		},
		{
			name:  "comments_and_blanks",
			input: "# top comment\n\n   \n# another",
			want:  nil,
		},
		{
			name:  "simple_glob",
			input: "*.log",
			want: []gregaleignorePattern{{
				raw: "*.log", globSegments: []string{"*.log"},
			}},
		},
		{
			name:  "anchored",
			input: "/build",
			want: []gregaleignorePattern{{
				raw: "build", anchor: true, globSegments: []string{"build"},
			}},
		},
		{
			name:  "dir_only",
			input: "build/",
			want: []gregaleignorePattern{{
				raw: "build", dirOnly: true, globSegments: []string{"build"},
			}},
		},
		{
			name:  "negate",
			input: "!keep.txt",
			want: []gregaleignorePattern{{
				raw: "keep.txt", negate: true, globSegments: []string{"keep.txt"},
			}},
		},
		{
			name:  "anchored_dir",
			input: "/build/",
			want: []gregaleignorePattern{{
				raw: "build", anchor: true, dirOnly: true,
				globSegments: []string{"build"},
			}},
		},
		{
			name:  "deep_path_segments",
			input: "a/b/*.tmp",
			want: []gregaleignorePattern{{
				raw: "a/b/*.tmp", globSegments: []string{"a", "b", "*.tmp"},
			}},
		},
		{
			name:  "stripped_empty_is_skipped",
			input: "!\n/\n",
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseGregaleignore([]byte(tc.input))
			if !equalPatterns(got, tc.want) {
				t.Errorf("parseGregaleignore(%q) = %+v, want %+v", tc.input, got, tc.want)
			}
		})
	}
}

func equalPatterns(a, b []gregaleignorePattern) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].raw != b[i].raw ||
			a[i].anchor != b[i].anchor ||
			a[i].dirOnly != b[i].dirOnly ||
			a[i].negate != b[i].negate ||
			!equalStrings(a[i].globSegments, b[i].globSegments) {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMatchGregaleignore pins the matching semantics: anchored
// patterns match only at the root, unanchored patterns match at any
// depth, and a later '!' can re-include a path previously excluded.
func TestMatchGregaleignore(t *testing.T) {
	cases := []struct {
		name     string
		patterns string
		path     string
		isDir    bool
		want     bool
	}{
		{
			name:     "no_patterns",
			patterns: "",
			path:     "dist/index.js", isDir: false,
			want: false,
		},
		{
			name:     "unanchored_match_root",
			patterns: "*.log",
			path:     "a.log", isDir: false,
			want: true,
		},
		{
			name:     "unanchored_match_nested",
			patterns: "*.log",
			path:     "deep/nested/b.log", isDir: false,
			want: true,
		},
		{
			name:     "unanchored_no_match_different_ext",
			patterns: "*.log",
			path:     "a.txt", isDir: false,
			want: false,
		},
		{
			name:     "anchored_match_root",
			patterns: "/build",
			path:     "build", isDir: false,
			want: true,
		},
		{
			name:     "anchored_no_match_nested",
			patterns: "/build",
			path:     "a/build", isDir: false,
			want: false,
		},
		{
			name:     "dir_only_skips_file",
			patterns: "build/",
			path:     "build", isDir: false,
			want: false,
		},
		{
			name:     "dir_only_matches_dir",
			patterns: "build/",
			path:     "build", isDir: true,
			want: true,
		},
		{
			name:     "deep_pattern_segments",
			patterns: "a/b/*.tmp",
			path:     "a/b/x.tmp", isDir: false,
			want: true,
		},
		{
			name:     "deep_pattern_no_match_other_subdir",
			patterns: "a/b/*.tmp",
			path:     "a/c/x.tmp", isDir: false,
			want: false,
		},
		{
			name:     "negate_re_includes",
			patterns: "*.log\n!keep.log",
			path:     "keep.log", isDir: false,
			want: false, // negated → re-included
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pats := parseGregaleignore([]byte(tc.patterns))
			got := matchGregaleignore(tc.path, tc.isDir, pats)
			if got != tc.want {
				t.Errorf("matchGregaleignore(%q, %v, %q) = %v, want %v",
					tc.path, tc.isDir, tc.patterns, got, tc.want)
			}
		})
	}
}

// TestPackDirToTarGz_BuildArtifactDefaults pins the six new
// default-excluded dirs (issue #1182 §3.5): dist, .next, coverage,
// target, .venv, .cache. Without these the tarball from a typical
// Next.js / Maven / Cargo project hits the SourceTarballMaxMB cap
// with garbage the server-side builder regenerates anyway.
func TestPackDirToTarGz_BuildArtifactDefaults(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Base(dir)
	writeFile(t, dir, "package.json", "{}")
	writeFile(t, dir, "src/index.js", "x")
	writeFile(t, dir, "dist/bundle.js", "compiled")
	writeFile(t, dir, ".next/build-manifest.json", "{}")
	writeFile(t, dir, "coverage/lcov.info", "data")
	writeFile(t, dir, "target/debug/binary", "compiled")
	writeFile(t, dir, ".venv/lib/python/x.py", "import")
	writeFile(t, dir, ".cache/pip/http/abc", "cached")

	dest := filepath.Join(t.TempDir(), "out.tar.gz")
	if _, err := packDirToTarGz(dir, dest, defaultZeroConfigSourceCapMB, nil); err != nil {
		t.Fatalf("pack: %v", err)
	}
	got := tarEntries(t, dest)

	// Kept (sanity):
	mustHave := []string{base + "/package.json", base + "/src/index.js"}
	for _, w := range mustHave {
		if !got[w] {
			t.Errorf("archive missing kept %q; entries: %v", w, got)
		}
	}
	// Dropped (defaults):
	dropPrefixes := []string{
		"/dist/", "/.next/", "/coverage/", "/target/", "/.venv/", "/.cache/",
	}
	for name := range got {
		for _, bad := range dropPrefixes {
			if strings.Contains(name, bad) {
				t.Errorf("archive should not contain %q (default-excluded); entries: %v", name, got)
			}
		}
	}
}

// TestPackDirToTarGz_Gregaleignore end-to-end: write a
// .gregaleignore with several patterns and assert the tarball drops
// (and re-includes via negate) accordingly. Pins the wire between
// packDirToTarGz and the parser.
func TestPackDirToTarGz_Gregaleignore(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Base(dir)
	writeFile(t, dir, "package.json", "{}")
	writeFile(t, dir, "src/keep.ts", "x")
	writeFile(t, dir, "scratch/notes.md", "drop me")
	writeFile(t, dir, "build/output.bin", "compiled")
	writeFile(t, dir, "src/skip.log", "drop me")
	writeFile(t, dir, "src/important.log", "re-include me")
	writeFile(t, dir, ".gregaleignore",
		"# drop scratch dirs and log files\n"+
			"scratch/\n"+
			"*.log\n"+
			"!src/important.log\n"+
			"/build\n",
	)

	dest := filepath.Join(t.TempDir(), "out.tar.gz")
	if _, err := packDirToTarGz(dir, dest, defaultZeroConfigSourceCapMB, nil); err != nil {
		t.Fatalf("pack: %v", err)
	}
	got := tarEntries(t, dest)

	mustHave := []string{
		base + "/package.json",
		base + "/src/keep.ts",
		base + "/src/important.log", // negated → re-included
	}
	for _, w := range mustHave {
		if !got[w] {
			t.Errorf("archive missing kept %q; entries: %v", w, got)
		}
	}
	for name := range got {
		for _, bad := range []string{"/scratch/", "/build/", "/src/skip.log"} {
			if strings.Contains(name, bad) {
				t.Errorf("archive should not contain %q (.gregaleignore); entries: %v", name, got)
			}
		}
	}
}
