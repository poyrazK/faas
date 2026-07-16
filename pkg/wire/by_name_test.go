// Tests for ListenOrRecreateByName and the (unexported) lookupUserUID /
// lookupGroupGID helpers, exercised through the exported surface.

package wire_test

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

func TestListenOrRecreateByName_Happy(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "byname.sock")

	// $USER is guaranteed to exist on the running account.
	userName := os.Getenv("USER")
	if userName == "" {
		t.Skip("USER not set")
	}

	// The function looks up the `faas` group, which is a production-only
	// system group (ADR-015). Dev / CI hosts do not have it, so skip the
	// happy path there; the unknown-group negative branch in
	// ListenOrRecreate's caller surface is covered separately.
	if _, ok := wire.LookupGroupGID("faas"); !ok {
		t.Skip("`faas` group not present (production-only)")
	}

	l, err := wire.ListenOrRecreateByName(sock, userName)
	if err != nil {
		t.Fatalf("ListenOrRecreateByName: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("stat: %v", err)
	}
}

func TestListenOrRecreateByName_UnknownUser(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "nope.sock")
	if _, err := wire.ListenOrRecreateByName(sock, "definitely_not_a_real_user_xyzzy"); err == nil {
		t.Fatal("unknown user should fail")
	}
}

func TestLookupGroupGID_KnownGroup(t *testing.T) {
	// The current user always has a primary group; resolve it and confirm
	// LookupGroupGID returns (>0, true) — the positive branch the existing
	// test (LookupGroupGID_KnownUnknowable) does not cover.
	u, err := user.Current()
	if err != nil {
		t.Skipf("user.Current unavailable: %v", err)
	}
	g, err := user.LookupGroupId(u.Gid)
	if err != nil {
		t.Skipf("primary group lookup failed: %v", err)
	}
	if gid, ok := wire.LookupGroupGID(g.Name); !ok || gid <= 0 {
		t.Errorf("LookupGroupGID(%q) = (%d, %v), want (>0, true)", g.Name, gid, ok)
	}
}