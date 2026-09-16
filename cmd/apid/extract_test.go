// cmd/apid/extract_test.go — regression coverage for extractTarGzInto and
// the post-join containment predicate. Pinned after the issue #432 phase 5
// local validation surfaced cmd/apid/extract.go:215's `filepath.IsLocal`
// mischeck: every nested archive entry was rejected on any box with an
// absolute FAAS_SCAN_SPOOL_ROOT (every Linux box, every Mac dev box) with
// `path escape after join rejected`. The check has been replaced with a
// `filepath.Rel`-based containment predicate (pathStaysUnder, mirroring
// pkg/rootfs/layer.go:148-165). The happy-path test below
// (TestExtractTarGz_AbsoluteSpool_NestedFilePasses) is the key regression
// case and fails with the old IsLocal-only check.
//
// Why no test existed before: the existing tarball handling tests
// (cmd/apid/deploy_inputs_test.go) call validateTarballShape, which only
// inspects the gzip stream and never reaches the post-join check inside
// extractTarGzInto. They never set FAAS_SCAN_SPOOL_ROOT — they set
// FAAS_SPOOL_ROOT. The post-join predicate was therefore untested prior to
// this PR.

package main

import (
	"archive/tar"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// safeLim mirrors the limits the production scan path uses when no plan
// envelope is available. Per-entry and total caps are generous so the
// tests below don't trip byte-cap races unrelated to the predicate.
var extractTestSafeLim = extractLimits{
	MaxEntries:    1000,
	MaxFileBytes:  1 << 20,
	MaxTotalBytes: 10 << 20,
}

// TestExtractTarGz_AbsoluteSpool_NestedFilePasses — the key regression
// case. The original extract.go:215 check rejected every nested entry
// on any absolute spool dir (the default on every Linux box). With the
// fix it must succeed.
func TestExtractTarGz_AbsoluteSpool_NestedFilePasses(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", dir)

	raw := buildTestTarGz(t,
		[]tar.Header{
			{Name: "project/"},
			{Name: "project/src/"},
			{Name: "project/src/index.js"},
		},
		map[string][]byte{
			"project/src/index.js": []byte("exports.handler = () => 1;\n"),
		})
	spool := filepath.Join(dir, "src-spool")
	if err := os.MkdirAll(spool, 0o755); err != nil {
		t.Fatalf("mkdir spool: %v", err)
	}
	srcPath := filepath.Join(spool, "input.tar.gz")
	if err := os.WriteFile(srcPath, raw, 0o644); err != nil {
		t.Fatalf("write tar: %v", err)
	}

	scanDir, prob := extractTarGzToDir(srcPath, extractTestSafeLim)
	if prob != nil {
		t.Fatalf("extract: code=%s detail=%s", prob.Code, prob.Detail)
	}
	defer func() { _ = os.RemoveAll(scanDir) }()

	extracted := filepath.Join(scanDir, "src", "index.js")
	body, err := os.ReadFile(extracted)
	if err != nil {
		t.Fatalf("nested file missing under scanDir=%s: %v", scanDir, err)
	}
	if string(body) != "exports.handler = () => 1;\n" {
		t.Errorf("body=%q want handler source", string(body))
	}
}

func TestExtractTarGz_LeadingDotWrapperIsCanonicalised(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", dir)
	raw := buildTestTarGz(t,
		[]tar.Header{
			{Name: "./project/compose.yaml"},
			{Name: "./project/services/api/Dockerfile"},
		},
		map[string][]byte{
			"./project/compose.yaml":            []byte("services: {}\n"),
			"./project/services/api/Dockerfile": []byte("FROM scratch\n"),
		})
	srcPath := filepath.Join(dir, "leading-dot.tar.gz")
	if err := os.WriteFile(srcPath, raw, 0o644); err != nil {
		t.Fatalf("write tar: %v", err)
	}

	scanDir, prob := extractTarGzToDir(srcPath, extractTestSafeLim)
	if prob != nil {
		t.Fatalf("extract: code=%s detail=%s", prob.Code, prob.Detail)
	}
	defer func() { _ = os.RemoveAll(scanDir) }()
	for _, name := range []string{"compose.yaml", "services/api/Dockerfile"} {
		if _, err := os.Stat(filepath.Join(scanDir, filepath.FromSlash(name))); err != nil {
			t.Fatalf("canonical file %q missing: %v", name, err)
		}
	}
}

// TestExtractTarGz_RejectsEscapeAndAbsolute — pins both traversal
// vectors that the upstream escapesArchiveRoot guard handles, so a
// future refactor that drops that guard is caught here too.
func TestExtractTarGz_RejectsEscapeAndAbsolute(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", dir)
	spool := filepath.Join(dir, "escape-spool")
	if err := os.MkdirAll(spool, 0o755); err != nil {
		t.Fatalf("mkdir spool: %v", err)
	}

	t.Run("parent_traversal", func(t *testing.T) {
		raw := buildTestTarGz(t,
			[]tar.Header{{Name: "../../etc/passwd"}},
			map[string][]byte{"../../etc/passwd": []byte("owned")})
		path := filepath.Join(spool, "esc.tar.gz")
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatalf("write tar: %v", err)
		}
		_, prob := extractTarGzToDir(path, extractTestSafeLim)
		prob = mustExtractProblem(t, prob, "expected rejection on '../' escape, got nil")
		if !strings.Contains(prob.Detail, "absolute paths or '..' entries are rejected") {
			t.Errorf("detail %q does not mention escapesArchiveRoot predicate", prob.Detail)
		}
	})

	t.Run("absolute_path", func(t *testing.T) {
		raw := buildTestTarGz(t,
			[]tar.Header{{Name: "/etc/passwd"}},
			map[string][]byte{"/etc/passwd": []byte("owned")})
		path := filepath.Join(spool, "abs.tar.gz")
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatalf("write tar: %v", err)
		}
		_, prob := extractTarGzToDir(path, extractTestSafeLim)
		if prob == nil {
			t.Fatal("expected rejection on absolute path, got nil")
		}
	})
}

// TestPathStaysUnder pins the containment predicate directly so the
// table covers cases that integration tests don't reach — sibling-prefix
// collisions (where a raw strings.HasPrefix check would silently accept
// a path outside the root), filesystem-root dst, trailing separator on
// dst, and the trivial target == root.
func TestPathStaysUnder(t *testing.T) {
	cases := []struct {
		name    string
		target  string
		root    string
		contain bool
	}{
		{"target_equal_root", "/tmp/root", "/tmp/root", true},
		{"nested_file", "/tmp/root/a/b/c.js", "/tmp/root", true},
		{"sibling_prefix", "/tmp/root-evil/x.js", "/tmp/root", false},
		{"parent_traversal", "/tmp/root/../escape", "/tmp/root", false},
		{"outside_root", "/etc/passwd", "/tmp/root", false},
		{"root_with_trailing_separator", "/tmp/root/a.js", "/tmp/root/", true},
		{"filesystem_root_dst", "/x/y.js", "/", true},
		{"nested_under_root_with_trailing_separator",
			"/tmp/root/a/b.js", "/tmp/root/", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pathStaysUnder(tc.target, tc.root)
			if got != tc.contain {
				t.Errorf("pathStaysUnder(target=%q, root=%q) = %v, want %v",
					tc.target, tc.root, got, tc.contain)
			}
		})
	}
}

// mustExtractProblem is the SA5011 escape hatch for the rejection
// tests: extractTarGzToDir can legitimately return (dir, nil) for a
// clean tarball, but these tests want a real Problem to assert
// against. A helper that t.Fatal()s and returns the value lets
// staticcheck see the value is non-nil at the call site.
func mustExtractProblem(t *testing.T, p *api.Problem, msg string) *api.Problem {
	t.Helper()
	if p == nil {
		t.Fatal(msg)
	}
	return p
}
