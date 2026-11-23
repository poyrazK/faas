package fcvm

import (
	"os"
	"path/filepath"
	"testing"
)

// adr: 20261122132055 — the load field follows the Firecracker release.
func TestDirtyTrackingLoadKey(t *testing.T) {
	for version, want := range map[string]string{
		"1.7.0": "enable_diff_snapshots", "1.10.0": "enable_diff_snapshots", "1.13.1": "enable_diff_snapshots",
		"1.14.0": "track_dirty_pages", "1.17.0": "track_dirty_pages", "": "enable_diff_snapshots",
	} {
		if got := DirtyTrackingLoadKey(version); got != want {
			t.Errorf("DirtyTrackingLoadKey(%q) = %q, want %q", version, got, want)
		}
	}
}

// adr: 20261122132055 — only sandbox sessions on an enabled VMM track dirty
// pages, and native recovery or sealed restores never do.
func TestSandboxDiffLoadKeyFor(t *testing.T) {
	session := RestoreSpec{sandboxSessionCapture: true}
	cases := []struct {
		name    string
		enabled bool
		spec    RestoreSpec
		native  bool
		want    string
	}{
		{"disabled", false, session, false, ""},
		{"session", true, session, false, "track_dirty_pages"},
		{"app wake", true, RestoreSpec{}, false, ""},
		{"native recovery", true, session, true, ""},
	}
	for _, tc := range cases {
		v := (&JailerVMM{}).WithSandboxDiffSnapshots(tc.enabled, "1.17.0")
		if got := v.sandboxDiff.loadKeyFor(tc.spec, tc.native); got != tc.want {
			t.Errorf("%s: loadKeyFor = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// adr: 20261122132055 — the parent is the restored image until a capture
// produces one; a capture without a keepable image makes the next one Full;
// an untracked instance is always Full.
func TestSandboxDiffParentLifecycle(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, memSnapshotName), []byte("restored"), 0o600); err != nil {
		t.Fatal(err)
	}
	var s sandboxDiffSnapshots
	if f, err := s.beginCapture("i1", root); err != nil || f != nil {
		t.Fatalf("untracked instance: parent %v, err %v; want Full", f, err)
	}
	s.loaded("i1")
	assertParent(t, &s, root, "restored")

	next := filepath.Join(t.TempDir(), "captured")
	if err := os.WriteFile(next, []byte("captured"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(next)
	if err != nil {
		t.Fatal(err)
	}
	s.captured("i1", f)
	// The held image outlives its path, as a published temp file does.
	if err := os.Remove(next); err != nil {
		t.Fatal(err)
	}
	assertParent(t, &s, root, "captured")
	assertParent(t, &s, root, "captured") // each capture gets its own handle

	s.captured("i1", nil)
	if f, err := s.beginCapture("i1", root); err != nil || f != nil {
		t.Fatalf("after a capture without an image: parent %v, err %v; want Full", f, err)
	}
	s.loaded("i1")
	s.forget("i1")
	if s.tracking("i1") {
		t.Fatal("forgotten instance still tracked")
	}
}

func assertParent(t *testing.T, s *sandboxDiffSnapshots, root, want string) {
	t.Helper()
	f, err := s.beginCapture("i1", root)
	if err != nil || f == nil {
		t.Fatalf("parent %v, err %v", f, err)
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 64)
	n, _ := f.ReadAt(buf, 0)
	if got := string(buf[:n]); got != want {
		t.Fatalf("parent holds %q, want %q", got, want)
	}
}
