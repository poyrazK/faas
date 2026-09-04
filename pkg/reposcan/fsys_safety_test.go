package reposcan

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// TestFSSafety_DotDotEscapeRejected — a fsys path containing
// a ".." element is fs.ValidPath() = false. fs.ReadFile would
// panic on such a path (a tarball entry like
// `subdir/../../../etc/passwd` is a classic CVE pattern). The
// scanner's readValidFile never reads such paths, and the broader
// Scan() function surfaces this as a non-nil error.
func TestFSSafety_DotDotEscapeRejected(t *testing.T) {
	t.Parallel()
	// Most fs.FS implementations refuse to even hand a path with
	// ".." to fs.Open; reading it would never reach the host. The
	// pathological case is when the implementation accepts the
	// path but reads something outside the archive.
	if fs.ValidPath("subdir/../escape.txt") {
		t.Errorf("fs.ValidPath accepted 'subdir/../escape.txt'")
	}
	_, err := readValidFile(fstest.MapFS{}, "subdir/../escape.txt")
	if err == nil {
		t.Errorf("readValidFile returned nil for '../' path")
	}
}

// TestFSSafety_AbsolutePathRejected — paths starting with "/"
// are host-rooted; the scanner must refuse.
func TestFSSafety_AbsolutePathRejected(t *testing.T) {
	t.Parallel()
	if fs.ValidPath("/etc/passwd") {
		t.Errorf("fs.ValidPath accepted '/etc/passwd'")
	}
	_, err := readValidFile(fstest.MapFS{}, "/etc/passwd")
	if err == nil {
		t.Errorf("readValidFile returned nil for absolute path")
	}
}

// TestFSSafety_TrailingSlashRejected — fs.ValidPath rejects
// trailing slashes (paths that look like directory references).
func TestFSSafety_TrailingSlashRejected(t *testing.T) {
	t.Parallel()
	if fs.ValidPath("subdir/") {
		t.Errorf("fs.ValidPath accepted trailing slash")
	}
	_, err := readValidFile(fstest.MapFS{}, "subdir/")
	if err == nil {
		t.Errorf("readValidFile returned nil for trailing-slash path")
	}
}

// TestFSSafety_EmptyPathRejected — fs.ValidPath rejects "".
func TestFSSafety_EmptyPathRejected(t *testing.T) {
	t.Parallel()
	if fs.ValidPath("") {
		t.Errorf("fs.ValidPath accepted empty path")
	}
	_, err := readValidFile(fstest.MapFS{}, "")
	if err == nil {
		t.Errorf("readValidFile returned nil for empty path")
	}
}

// TestFSSafety_NoSymlinkLeak — even a MapFS that registers a
// file at the EXACT path requested, the scanner can't be tricked
// into reading /etc/passwd because it never calls os.Open.
// We exercise this by attempting a read of a path the MapFS
// DOES have (should succeed) and a path it does NOT (should fail).
func TestFSSafety_NoSymlinkLeak(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch")},
	}
	body, err := readValidFile(fsys, "Dockerfile")
	if err != nil || string(body) != "FROM scratch" {
		t.Errorf("readValidFile(Dockerfile) = (%v, %v); want (nil, FROM scratch)", body, err)
	}
	_, err = readValidFile(fsys, "/etc/passwd")
	if err == nil {
		t.Errorf("readValidFile(/etc/passwd) returned nil despite ValidPath=false")
	}
}

// TestFSSafety_ReadFirstValidFileSkipsMissing — when a
// candidates list contains a mix of present and absent files,
// the FIRST present file wins. The escape paths in candidates
// are rejected on fs.ValidPath first.
func TestFSSafety_ReadFirstValidFileSkipsMissing(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"fly.toml": &fstest.MapFile{Data: []byte("app = \"x\"\n")},
	}
	body, src, err := readFirstValidFile(fsys, []string{"render.yaml", "fly.toml"})
	if err != nil {
		t.Fatalf("readFirstValidFile: %v", err)
	}
	if src != "fly.toml" || !strings.HasPrefix(string(body), "app = ") {
		t.Errorf("readFirstValidFile picked (%s, %q)", src, body)
	}
	// Empty result on all-missing.
	body2, src2, err2 := readFirstValidFile(fsys, []string{"nope.yaml", "also-not.yaml"})
	if err2 != nil || body2 != nil || src2 != "" {
		t.Errorf("readFirstValidFile on all-missing = (%v, %q, %v); want (nil, \"\", nil)",
			body2, src2, err2)
	}
}

// TestFSSafety_Scan_NoEscapePossible — the Scan() entry point
// never returns a workload whose source string contains "../".
// An empty archive is not a deployable root application and must
// therefore produce no synthetic workload.
func TestFSSafety_Scan_NoEscapePossible(t *testing.T) {
	t.Parallel()
	r, err := Scan(fstest.MapFS{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, w := range r.Workloads {
		if strings.Contains(w.Source, "..") {
			t.Errorf("workload source contains '..': %q", w.Source)
		}
	}
	if len(r.Workloads) != 0 {
		t.Errorf("empty archive produced workloads = %v", r.Workloads)
	}
	if r.Tier != 0 {
		t.Errorf("empty archive tier = %s, want unknown", r.Tier)
	}
}

// TestFSSafety_ReadValidFile_RejectsDirectory — readValidFile
// must propagate ErrInvalid (via errors.Is) when handed a
// directory path. The fsys_safety.go helper classifies the
// ErrInvalid case as "next candidate" only inside
// readFirstValidFile — readValidFile surfaces it. If a future
// refactor stops surfacing it, the silent-skip path in
// detectCompose would skip a directory-shaped file and miss
// the actual fsys content.
func TestFSSafety_ReadValidFile_RejectsDirectory(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"subdir": &fstest.MapFile{Mode: 0o755 | fs.ModeDir},
	}
	_, err := readValidFile(fsys, "subdir")
	if err == nil {
		t.Fatalf("readValidFile(/) returned nil err")
	}
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("readValidFile returned %v; want errors.Is fs.ErrInvalid", err)
	}
}

// TestFSSafety_ReadFirstValidFile_SkipsDirectory — confirms
// readFirstValidFile classifies the directory case as "next
// candidate" (NOT a propagated error). Then the equivalent
// non-dir file is read successfully.
func TestFSSafety_ReadFirstValidFile_SkipsDirectory(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"subdir":   &fstest.MapFile{Mode: 0o755 | fs.ModeDir},
		"goodfile": &fstest.MapFile{Data: []byte("hi")},
	}
	body, src, err := readFirstValidFile(fsys, []string{"subdir", "goodfile"})
	if err != nil {
		t.Errorf("readFirstValidFile: %v", err)
	}
	if string(body) != "hi" || src != "goodfile" {
		t.Errorf("readFirstValidFile = (%s, %q)", src, body)
	}
}
