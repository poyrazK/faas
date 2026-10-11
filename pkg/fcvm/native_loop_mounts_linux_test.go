//go:build linux

// adr: 568 — mount proofs must exclude replaced, unsafe and nested resources.
package fcvm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestNativeLoopDetachConfirmationIsBoundedAndObservesOnly(t *testing.T) {
	observations := 0
	if err := waitNativeLoopDetached(t.Context(), func() (bool, error) {
		observations++
		return observations < 3, nil
	}); err != nil || observations != 3 {
		t.Fatal("delayed original detach was not confirmed", observations, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	if err := waitNativeLoopDetached(ctx, func() (bool, error) { return true, nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("retained original attachment granted retirement", err)
	}
	injected := errors.New("original loop identity changed")
	if err := waitNativeLoopDetached(t.Context(), func() (bool, error) { return false, injected }); !errors.Is(err, injected) {
		t.Fatal("failed inspection supplied detach proof", err)
	}
	canceled, stop := context.WithCancel(t.Context())
	if err := waitNativeLoopDetached(canceled, func() (bool, error) { stop(); return false, nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation during observation supplied success", err)
	}
}

func TestNativeLoopMountInfoRequiresExactPrivateExt4Identity(t *testing.T) {
	point := "/private/point"
	good := "101 2 7:3 / /private/point rw,nosuid,nodev,noexec,relatime - ext4 /dev/loop3 rw\n"
	entry, err := parseNativeLoopMount([]byte(good), point)
	if err != nil || entry == nil || entry.id != 101 || entry.device != unix.Mkdev(7, 3) {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
	for _, bad := range []string{
		strings.Replace(good, "nosuid,", "", 1),
		strings.Replace(good, "nodev,", "", 1),
		strings.Replace(good, "noexec,", "", 1),
		strings.Replace(good, " / /private/point ", " /subtree /private/point ", 1),
		strings.Replace(good, "- ext4", "- tmpfs", 1),
		strings.Replace(good, "101 2", "0 2", 1),
		strings.Replace(good, "7:3", "broken", 1),
		good + good,
		good + strings.Replace(good, "/private/point", "/private/point/nested", 1),
	} {
		if _, err := parseNativeLoopMount([]byte(bad), point); err == nil {
			t.Fatal("ambiguous or unsafe mount supplied proof:", bad)
		}
	}
	if entry, err := parseNativeLoopMount([]byte(strings.Replace(good, point, "/unrelated", 1)), point); err != nil || entry != nil {
		t.Fatalf("unrelated mount=%+v %v", entry, err)
	}
}

func TestNativeLoopStatusRequiresOriginalTokenBackingAndFlags(t *testing.T) {
	id := "bd563861-501b-4ac2-9b4a-e2b782b3c0ad"
	record := nativeLoopMountRecord{ID: id, Device: nativeLoopDevice{Number: 3, Source: nativeLoopIdentity{Device: 17, Inode: 42}}}
	good := unix.LoopInfo64{Number: 3, Device: 17, Inode: 42, Flags: unix.LO_FLAGS_AUTOCLEAR}
	copy(good.File_name[:], nativeLoopMarker+id)
	if !matchesNativeLoop(&good, record) {
		t.Fatal("complete ownership did not match")
	}
	for _, change := range []func(*unix.LoopInfo64){
		func(info *unix.LoopInfo64) { info.File_name[0] = 'x' },
		func(info *unix.LoopInfo64) { info.Device++ },
		func(info *unix.LoopInfo64) { info.Inode++ },
		func(info *unix.LoopInfo64) { info.Number++ },
		func(info *unix.LoopInfo64) { info.Flags = 0 },
		func(info *unix.LoopInfo64) { info.Flags |= unix.LO_FLAGS_READ_ONLY },
		func(info *unix.LoopInfo64) { info.Offset = 1 },
		func(info *unix.LoopInfo64) { info.Sizelimit = 1 },
	} {
		bad := good
		change(&bad)
		if matchesNativeLoop(&bad, record) {
			t.Fatal("changed loop identity supplied cleanup authority")
		}
	}
}
