//go:build linux

package fcvm

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"golang.org/x/sys/unix"
)

func nativeRestoreChannelsOwnerFixture(t *testing.T) (*nativeLoadSequenceFixture, *nativeQualificationRestoreChannels) {
	t.Helper()
	f := nativeRestoreLoadSequenceFixture(t)
	loaded, err := f.v.loadNativeQualificationRestore(f.ctx, f.owner.Lease)
	if err != nil {
		t.Fatal(err)
	}
	permit := f.ctx.Value(nativeQualificationRestoreLoadContextKey{}).(*nativeQualificationRestoreLoadPermit)
	return f, &nativeQualificationRestoreChannels{v: f.v, ctx: f.ctx, owner: f.owner, target: f.target, permit: permit, loaded: loaded}
}

func TestNativeQualificationRestoreChannelRechecksOriginalTargetAndEvidence(t *testing.T) {
	for _, change := range []string{"original", "recovered", "revoked", "pid", "lease", "target", "load", "missing_load", "capture", "backings", "image", "lost_completion", "canceled"} {
		t.Run(change, func(t *testing.T) {
			f, channels := nativeRestoreChannelsOwnerFixture(t)
			ctx := f.ctx
			switch change {
			case "recovered":
				delete(f.v.nativeRecovery.owned, f.owner.Lease.Instance)
			case "revoked":
				if _, err := f.q.restores().revoke(ctx, f.target.Execution); err != nil {
					t.Fatal(err)
				}
			case "pid", "lease":
				owner := f.owner
				if change == "pid" {
					owner.StartTime++
				} else {
					owner.Lease.MemoryMaxMiB++
				}
				if err := f.q.owner.write(owner); err != nil {
					t.Fatal(err)
				}
			case "target":
				target := f.target
				target.Deadline = target.Deadline.Add(-1)
				if err := f.q.restores().write(target); err != nil {
					t.Fatal(err)
				}
			case "load":
				loaded := channels.loaded
				loaded.Cgroup.Inode++
				if err := f.loads.write(loaded); err != nil {
					t.Fatal(err)
				}
			case "missing_load":
				path, _ := f.loads.path(f.owner.Lease.Instance)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "image":
				images := nativeImageSourceJournal{owner: f.q.owner, backend: f.b}
				records, err := images.records()
				if err != nil {
					t.Fatal(err)
				}
				for _, record := range records {
					if sameNativeImageOwner(record.References[0], f.owner) {
						record.References[0].Ready = false
						if err := images.write(record); err != nil {
							t.Fatal(err)
						}
						break
					}
				}
			case "capture", "backings":
				capture, err := f.q.restores().requireCapture(ctx, f.target)
				if err != nil {
					t.Fatal(err)
				}
				if change == "capture" {
					capture.Info.StoredBytes++
					source, readErr := f.q.read(f.target.Execution.CaptureInstanceID)
					if readErr != nil {
						t.Fatal(readErr)
					}
					err = f.q.writeCapture(source, capture)
				} else {
					backings, readErr := f.q.readBackings(capture)
					if readErr != nil {
						t.Fatal(readErr)
					}
					backings.Images[0].Identity.Inode++
					path, pathErr := f.q.backingPath(capture.InstanceID)
					if pathErr != nil {
						t.Fatal(pathErr)
					}
					// Fault injection bypasses the immutable backing publisher.
					err = writeNativeJournalValue(path, backings)
				}
				if err != nil {
					t.Fatal(err)
				}
			case "lost_completion":
				channels.permit.completed.Store(false)
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := channels.requireOwner(ctx); (err == nil) != (change == "original") {
				t.Fatal(change, err)
			}
		})
	}
}

func TestNativeQualificationRestoreChannelCannotBorrowRetainedHookAcknowledgement(t *testing.T) {
	f, channels := nativeRestoreChannelsOwnerFixture(t)
	handler := func(context.Context, state.EnvironmentQualificationExecution, net.Conn) error { return nil }
	handlers := make(map[uint32]nativeQualificationRestoreStreamHandler)
	for _, port := range nativeRestoreChannelPorts() {
		handlers[port] = handler
	}
	// This has the same private on-disk target and inherited record, but no
	// original process-local acknowledgement. It cannot publish a socket.
	ctx := nativeQualificationRestoreContext(t.Context(), f.target)
	if _, err := f.v.prepareNativeQualificationRestoreChannels(ctx, f.owner.Lease, handlers); err == nil {
		t.Fatal("retained evidence recreated original channel producer")
	}
	channels.permit.completed.Store(false)
	if _, err := f.v.prepareNativeQualificationRestoreChannels(f.ctx, f.owner.Lease, handlers); err == nil {
		t.Fatal("lost original hook acknowledgement opened endpoints")
	}
}

// Real Unix inode/permission checks; VM process/lease authority is covered
// independently above and by the actual dedicated native VM acceptance.
func TestNativeQualificationRestoreChannelRejectsReplacedNamespaceOrEndpoint(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "nrc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	v := &JailerVMM{chrootBase: base, fcName: "f"}
	owner := nativeLaunchRecord{Lease: Lease{Instance: "target", UID: os.Geteuid(), GID: os.Getegid()}}
	root := v.chrootRoot(owner.Lease.Instance)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(fd), "original-root")
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		t.Fatal(err)
	}
	c := &nativeQualificationRestoreChannels{v: v, owner: owner, root: file, identity: nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}}
	path := v.guestVsockUDSSock(owner.Lease.Instance, VsockRuntimeConfigHostPort)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	defer listener.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	original, err := c.endpointIdentity(VsockRuntimeConfigHostPort)
	if err := errors.Join(err, c.requireRoot()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := c.endpointIdentity(VsockRuntimeConfigHostPort); err == nil {
		t.Fatal("public endpoint retained private authority")
	}
	if err := os.Rename(path, path+"-original"); err != nil {
		t.Fatal(err)
	}
	other, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	replacement, err := c.endpointIdentity(VsockRuntimeConfigHostPort)
	if err != nil || replacement == original {
		t.Fatal("replacement endpoint borrowed original inode", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatal("late original close unlinked replacement endpoint", err)
	}
	if err := os.Rename(root, filepath.Join(filepath.Dir(root), "retained-root")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := c.requireRoot(); err == nil {
		t.Fatal("replacement jail directory borrowed pinned namespace")
	}
}
