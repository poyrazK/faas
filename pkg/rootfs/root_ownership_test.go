package rootfs

import (
	"archive/tar"
	"testing"
)

// adr: 1239 — with root preservation on (imaged holding CAP_CHOWN), entries
// owned by uid or gid 0 are published as root instead of being left under the
// unprivileged daemon's uid; mkfs.ext4 -d copies staging owners as-is.
func TestRootOwnershipIsPreservedWhenEnabled(t *testing.T) {
	defer SetPreserveRootOwnership(false)
	for _, tc := range []struct {
		name             string
		hdr              tar.Header
		enabled          bool
		wantUID, wantGID int
		wantPreserved    bool
	}{
		{"root, enabled", tar.Header{Uid: 0, Gid: 0}, true, 0, 0, true},
		{"named root, enabled", tar.Header{Uid: 0, Gid: 0, Uname: "root", Gname: "root"}, true, 0, 0, true},
		{"user with root group, enabled", tar.Header{Uid: 1001, Gid: 0}, true, 1001, 0, true},
		{"named non-root uid 0, enabled", tar.Header{Uid: 0, Gid: 0, Uname: "node"}, true, 0, 0, false},
		{"root, disabled", tar.Header{Uid: 0, Gid: 0}, false, 0, 0, false},
		{"user with root group, disabled", tar.Header{Uid: 1001, Gid: 0}, false, 0, 0, false},
		{"plain user, disabled", tar.Header{Uid: 1001, Gid: 1001}, false, 1001, 1001, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			SetPreserveRootOwnership(tc.enabled)
			hdr := tc.hdr
			uid, gid, ok := parseOwnership(&hdr)
			if ok != tc.wantPreserved || (ok && (uid != tc.wantUID || gid != tc.wantGID)) {
				t.Fatalf("parseOwnership = %d:%d %v, want %d:%d %v", uid, gid, ok, tc.wantUID, tc.wantGID, tc.wantPreserved)
			}
		})
	}
}
