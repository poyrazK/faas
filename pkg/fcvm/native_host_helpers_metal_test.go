//go:build linux && metal

// adr: 459 — native helper descendants remain owned until kernel exit proof.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func TestMetalNativeHostHelperRetiresOrphanDescendant(t *testing.T) {
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Skip("requires the dedicated native x86_64 Linux acceptance host and root")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skip("requires the dedicated native KVM acceptance host")
	}
	binary := os.Getenv("FAAS_TEST_VMMD_BINARY")
	if binary == "" {
		t.Skip("run make test-metal with its release-matched jail helper")
	}
	helper, err := resolveMountHelper(binary)
	if err != nil {
		t.Fatal(err)
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	ownerJournal := &nativeLaunchJournal{root: filepath.Join(t.TempDir(), "journal")}
	lease := leaseForSlot("native-helper-descendant", 0)
	if err := ownerJournal.prepare(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	owner, err := ownerJournal.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	j := &nativeHostHelperJournal{owner: ownerJournal, groups: newNativeHostHelperGroups()}
	dir := t.TempDir()
	descendantMarker, release := filepath.Join(dir, "descendant"), filepath.Join(dir, "release")
	// The production launch helper execs this trusted acceptance command.
	// The descendant stays alive after the leader exits and inherits its group.
	script := `/bin/sleep 300 >/dev/null 2>&1 & printf '%s' "$!" > "$1"; while [ ! -e "$2" ]; do /bin/sleep 0.01; done`
	cmd, err := newNativeHostHelperCommand(helper, []string{"/bin/sh", "-c", script, "native-helper-test", descendantMarker, release})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := j.retireAll(ctx, owner); err != nil {
			t.Error("native helper cleanup:", err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- j.run(ctx, owner, cmd, 5*time.Second) }()
	pid, err := strconv.Atoi(string(waitNativeHelperFile(t, descendantMarker)))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := openNativeProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	frames, err := j.records(owner)
	if err != nil || len(frames) != 1 {
		t.Fatalf("native frame=%+v err=%v", frames, err)
	}
	changed := frames[0].Group
	changed.Inode++
	if err := j.groups.Retire(ctx, changed); err == nil {
		t.Fatal("changed cgroup identity authorized a kill")
	}
	liveCtx, liveCancel := context.WithTimeout(ctx, 20*time.Millisecond)
	err = handle.Wait(liveCtx)
	liveCancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("changed identity killed the original descendant: %v", err)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := handle.Wait(ctx); err != nil {
		t.Fatal("orphan descendant survived helper retirement:", err)
	}
	if err := j.requireRemoved(owner); err != nil {
		t.Fatal(err)
	}
}
