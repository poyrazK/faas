package rootfs

import (
	"archive/tar"
	"testing"
)

func TestFullRootfsZeroOwnership(t *testing.T) {
	resolver := NewPasswdResolver(map[string]PasswdEntry{"node": {Uid: 1001, Gid: 1001}, "root": {Uid: 0, Gid: 0}})
	for _, tc := range []struct {
		name             string
		uid, gid         int
		uname, gname     string
		wantUID, wantGID int
		ok               bool
	}{
		{"nonroot-in-root-group", 101, 0, "nginx", "root", 101, 0, true},
		{"root-with-empty-names", 0, 0, "", "", 0, 0, true},
		{"root-with-names", 0, 0, "root", "root", 0, 0, true},
		{"zero-uid-nonzero-gid", 0, 101, "", "", 0, 101, true},
		{"named-user-root-group", 0, 0, "node", "root", 1001, 0, true},
		{"uid-out-of-range", 65535, 0, "", "", 0, 0, false},
		{"gid-out-of-range", 101, 65535, "", "", 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uid, gid, ok := fullRootfsOwnership(&tar.Header{Uid: tc.uid, Gid: tc.gid, Uname: tc.uname, Gname: tc.gname}, resolver)
			if uid != tc.wantUID || gid != tc.wantGID || ok != tc.ok {
				t.Fatalf("got %d:%d/%v want %d:%d/%v", uid, gid, ok, tc.wantUID, tc.wantGID, tc.ok)
			}
		})
	}
}
