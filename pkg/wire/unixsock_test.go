// Tests for the unix-socket helpers. We test ListenOrRecreate's full happy
// path on a real loopback unix socket (no KVM needed), plus the validation
// negatives (non-absolute path, bad mode, stale-socket recreation). The
// chown-then-chmod path is exercised by calling with the current uid/gid
// (so the calls don't require CAP_CHOWN) and asserting the resulting mode.
//
// Note: macOS imposes a ~104-byte sun_path limit (sockaddr_un.sun_path
// [104]char); Linux is 108. We keep test paths short to stay portable.

package wire_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

// shortDir returns a directory whose absolute path is short enough for
// sockaddr_un.sun_path on every Unix the platform suite runs on. The
// returned socket path is at most ~70 bytes long.
func shortDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir() // e.g. /tmp/TestX... — already long on macOS.
	// Force path length down by symlinking into a shorter parent. This is
	// best-effort; if symlinks fail (non-Linux/macOS), fall back to t.TempDir.
	short := "/tmp/fwst-" + t.Name()
	if err := os.Symlink(root, short); err != nil {
		return root
	}
	t.Cleanup(func() { _ = os.Remove(short) })
	return short
}

func TestListenOrRecreate_RoundTrip(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "vmmd.sock")

	l, err := wire.ListenOrRecreate(sock, 0, 0, 0o660)
	if err != nil {
		t.Fatalf("ListenOrRecreate: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	// Confirm a connection actually works — guards against silent bind
	// failures that manifest only under real load.
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = c.Close()

	// 0660 is settable by everyone (not just root), so we can verify the
	// path. Chown-to-self is a no-op but should not error.
	st, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := st.Mode().Perm(); got != 0o660 {
		t.Fatalf("permission = %o, want 0o660", got)
	}
}

func TestListenOrRecreate_RecreatesStaleSocket(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "vmmd.sock")

	if err := os.MkdirAll(filepath.Dir(sock), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Plant a stale file where the socket should go. (A real socket file
	// can't be planted without binding, but our helper should treat any
	// existing path at that location as stale.)
	if err := os.WriteFile(sock, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("plant stale file: %v", err)
	}

	l, err := wire.ListenOrRecreate(sock, 0, 0, 0o660)
	if err != nil {
		t.Fatalf("ListenOrRecreate should remove stale file: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket missing: %v", err)
	}
}

func TestListenOrRecreate_RejectsRelativePath(t *testing.T) {
	if _, err := wire.ListenOrRecreate("vmmd.sock", 0, 0, 0o660); err == nil {
		t.Fatalf("relative path should be rejected")
	}
}

func TestListenOrRecreate_RejectsBadMode(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "vmmd.sock")
	if _, err := wire.ListenOrRecreate(sock, 0, 0, 0); err == nil {
		t.Fatalf("zero mode should be rejected")
	}
	if _, err := wire.ListenOrRecreate(sock, 0, 0, os.FileMode(0o1000)); err == nil {
		t.Fatalf("out-of-range mode should be rejected")
	}
}

func TestParseUIDGID_RoundTrip(t *testing.T) {
	for _, tc := range []struct {
		s       string
		uid, gid int
		wantErr bool
	}{
		{"1000:2000", 1000, 2000, false},
		{"0:0", 0, 0, false},
		{"root:wheel", 0, 0, true},
		{"1000", 0, 0, true},
		{":", 0, 0, true},
	} {
		gotUID, gotGID, err := wire.ParseUIDGID(tc.s)
		if (err != nil) != tc.wantErr {
			t.Errorf("%q: wantErr=%v got err=%v", tc.s, tc.wantErr, err)
		}
		if err != nil {
			continue
		}
		if gotUID != tc.uid || gotGID != tc.gid {
			t.Errorf("%q: got %d:%d, want %d:%d", tc.s, gotUID, gotGID, tc.uid, tc.gid)
		}
	}
}

func TestLookupGroupGID_KnownUnknowable(t *testing.T) {
	// We can't know what groups exist on the host, but "definitely_not_a_real_group_xyzzy"
	// is guaranteed absent. This guards the negative path; the positive path
	// is covered by the integration smoke at PR-9.
	if _, ok := wire.LookupGroupGID("definitely_not_a_real_group_xyzzy"); ok {
		t.Fatalf("bogus group name should not resolve")
	}
}
