//go:build linux

package main

import (
	"os"
	"syscall"
	"testing"
)

// The pipe inode must belong to the workload user: reopening /dev/stderr
// (/proc/self/fd/2) as that user checks the inode owner.
func TestWorkloadOutputPipeIsOwnedByTheWorkloadUser(t *testing.T) {
	uid, gid := os.Getuid(), os.Getgid()
	if uid == 0 && gid == 0 {
		uid, gid = 1000, 1000 // root: chown to the default app user
	}
	pipe, err := newWorkloadOutputPipe(&lockedBuffer{}, uid, gid)
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.finish()
	var st syscall.Stat_t
	if err := syscall.Fstat(int(pipe.w.Fd()), &st); err != nil {
		t.Fatal(err)
	}
	if int(st.Uid) != uid || int(st.Gid) != gid {
		t.Fatalf("pipe owned by %d:%d, want %d:%d", st.Uid, st.Gid, uid, gid)
	}
}
