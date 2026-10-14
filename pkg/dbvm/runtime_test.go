// adr: 1027
package dbvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

type fakeLeaser struct {
	mu       sync.Mutex
	held     map[string]fcvm.Lease
	released []string
}

func (f *fakeLeaser) Acquire(instance string) (fcvm.Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held == nil {
		f.held = map[string]fcvm.Lease{}
	}
	if _, ok := f.held[instance]; ok {
		return fcvm.Lease{}, errors.New("already leased")
	}
	l := fcvm.Lease{Instance: instance, Slot: len(f.held), UID: 29990 + len(f.held), GID: 29990 + len(f.held)}
	f.held[instance] = l
	return l, nil
}

func (f *fakeLeaser) Release(instance string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.held, instance)
	f.released = append(f.released, instance)
	return nil
}

const testID = "0f8fad5b-d9cb-469f-a165-70867728950e"

func testRuntime(t *testing.T, leases *fakeLeaser) *Runtime {
	t.Helper()
	dir := t.TempDir()
	fc := filepath.Join(dir, "firecracker-v1.7.0")
	if err := os.WriteFile(fc, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rt, err := NewRuntime(RuntimeConfig{
		JailBase: filepath.Join(dir, "dbjail"), VolumeRoot: filepath.Join(dir, "volumes"),
		KernelPath: filepath.Join(dir, "vmlinux"), Bases: map[int]string{17: filepath.Join(dir, "base.ext4")},
		ParentCgroup: "faas.slice/faas-db.slice", FirecrackerBin: fc, Leases: leases,
	})
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

func TestNewRuntimeRejectsIncompleteOrSharedJail(t *testing.T) {
	if _, err := NewRuntime(RuntimeConfig{}); err == nil {
		t.Fatal("empty config accepted")
	}
	_, err := NewRuntime(RuntimeConfig{
		JailBase: fcvm.JailChrootBase, VolumeRoot: "/v", KernelPath: "/k", Bases: map[int]string{17: "/b"},
		ParentCgroup: "p", Leases: &fakeLeaser{},
	})
	if err == nil {
		t.Fatal("database VMs accepted the app-VM jail base")
	}
}

// TestNewRuntimeRejectsAJailTooDeepForTheArchiveRelay: the relay socket
// lives inside each jail, and Linux caps unix socket paths at 107 bytes.
func TestNewRuntimeRejectsAJailTooDeepForTheArchiveRelay(t *testing.T) {
	dir := t.TempDir()
	fc := filepath.Join(dir, "firecracker")
	if err := os.WriteFile(fc, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := RuntimeConfig{
		VolumeRoot: "/v", KernelPath: "/k", Bases: map[int]string{17: "/b"}, ParentCgroup: "p",
		FirecrackerBin: fc, Leases: &fakeLeaser{}, ArchiveSocket: "/run/faas-dbbackupd/dbbackupd.sock",
	}
	cfg.JailBase = "/srv/fc/dbjail"
	if _, err := NewRuntime(cfg); err != nil {
		t.Fatalf("production jail base rejected: %v", err)
	}
	cfg.JailBase = "/mnt/e2e-scratch/dbvm-dev/a-jail-base-named-far-too-long"
	if _, err := NewRuntime(cfg); err == nil {
		t.Fatal("a jail base too deep for the relay socket was accepted")
	}
	cfg.ArchiveSocket = ""
	if _, err := NewRuntime(cfg); err != nil {
		t.Fatalf("without archiving the depth does not matter: %v", err)
	}
}

func TestRuntimePathsAndJailNaming(t *testing.T) {
	rt := testRuntime(t, &fakeLeaser{})
	if got := rt.VolumePath(testID); got != filepath.Join(rt.cfg.VolumeRoot, testID+".img") {
		t.Fatalf("VolumePath = %s", got)
	}
	// jailer names the chroot after the real firecracker basename.
	if got := rt.jailDir("db-" + testID); got != filepath.Join(rt.cfg.JailBase, "firecracker-v1.7.0", "db-"+testID) {
		t.Fatalf("jailDir = %s", got)
	}
}

func TestRuntimeRejectsBadRequestsBeforeLeasing(t *testing.T) {
	leases := &fakeLeaser{}
	rt := testRuntime(t, leases)
	ctx := context.Background()
	cases := map[string]BootRequest{
		"invalid id":     {ID: "../etc", Major: 17, VcpuCount: 1, MemSizeMiB: 256},
		"unknown major":  {ID: testID, Major: 16, VcpuCount: 1, MemSizeMiB: 256},
		"missing volume": {ID: testID, Major: 17, VcpuCount: 1, MemSizeMiB: 256},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if err := rt.Start(ctx, req); err == nil {
				t.Fatal("Start accepted the request")
			}
		})
	}
	if len(leases.held) != 0 || len(leases.released) != 0 {
		t.Fatalf("leases touched before validation: held=%v released=%v", leases.held, leases.released)
	}
}

func TestRuntimeFailedStartReleasesEverything(t *testing.T) {
	leases := &fakeLeaser{}
	rt := testRuntime(t, leases)
	if err := os.MkdirAll(rt.cfg.VolumeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.VolumePath(testID), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.reserve(testID); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.reserve(testID); !errors.Is(err, ErrRunning) {
		t.Fatalf("second reserve = %v, want ErrRunning", err)
	}
	if err := rt.DeleteVolume(context.Background(), testID); !errors.Is(err, ErrRunning) {
		t.Fatalf("DeleteVolume while running = %v, want ErrRunning", err)
	}
	rt.mu.Lock()
	vm := rt.vms[testID]
	rt.mu.Unlock()
	rt.teardown(testID, vm)
	if rt.Running(testID) || len(leases.held) != 0 || len(leases.released) != 1 {
		t.Fatalf("teardown left state: running=%v held=%v released=%v", rt.Running(testID), leases.held, leases.released)
	}
	if err := rt.DeleteVolume(context.Background(), testID); err != nil {
		t.Fatalf("DeleteVolume after teardown: %v", err)
	}
	if err := rt.DeleteVolume(context.Background(), testID); err != nil {
		t.Fatalf("DeleteVolume of a missing volume must converge: %v", err)
	}
}

func TestRuntimeControlRequiresRunningVM(t *testing.T) {
	rt := testRuntime(t, &fakeLeaser{})
	if _, err := rt.Dial(context.Background(), testID); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Dial = %v, want ErrNotRunning", err)
	}
	if err := rt.Stop(context.Background(), testID); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Stop = %v, want ErrNotRunning", err)
	}
}

func TestCmdlineHasJailID(t *testing.T) {
	jail := "db-" + testID
	cases := []struct {
		cmdline string
		want    bool
	}{
		{"firecracker\x00--id\x00" + jail + "\x00--config-file\x00vmconfig.json\x00", true},
		{"jailer\x00--id\x00" + jail + "\x00", true},
		{"firecracker\x00--id\x00" + jail + "-other\x00", false},
		{"firecracker\x00" + jail + "\x00", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := cmdlineHasJailID([]byte(tc.cmdline), jail); got != tc.want {
			t.Fatalf("cmdlineHasJailID(%q) = %v, want %v", tc.cmdline, got, tc.want)
		}
	}
}

func TestListVolumes(t *testing.T) {
	rt := testRuntime(t, &fakeLeaser{})
	if vols, err := rt.ListVolumes(); err != nil || len(vols) != 0 {
		t.Fatalf("empty node = %v, %v", vols, err)
	}
	if err := os.MkdirAll(rt.cfg.VolumeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{testID + ".img", "not-an-id.img", testID + ".img.tmp"} {
		if err := os.WriteFile(filepath.Join(rt.cfg.VolumeRoot, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	vols, err := rt.ListVolumes()
	if err != nil || len(vols) != 1 || vols[0].ID != testID || vols[0].Running {
		t.Fatalf("volumes = %+v, %v", vols, err)
	}
}

// GrowVolume refuses what it must before touching the file; the grow
// itself runs on metal (e2fsprogs).
func TestGrowVolumeRefusals(t *testing.T) {
	rt := testRuntime(t, &fakeLeaser{})
	ctx := context.Background()
	if err := rt.GrowVolume(ctx, testID, 2<<30); !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("grow a missing volume = %v", err)
	}
	if err := os.MkdirAll(rt.cfg.VolumeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	path := rt.VolumePath(testID)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, 2<<30); err != nil {
		t.Fatal(err)
	}
	if err := rt.GrowVolume(ctx, testID, 1<<30); !errors.Is(err, ErrVolumeShrink) {
		t.Fatalf("shrink = %v", err)
	}
	if err := rt.GrowVolume(ctx, testID, MaxVolumeBytes+1); err == nil {
		t.Fatal("a volume past the maximum was accepted")
	}
	if info, _ := os.Stat(path); info.Size() != 2<<30 {
		t.Fatalf("a refused grow changed the file: %d", info.Size())
	}
}

// A guest flooding its console never grows the log past its cap; the log
// keeps the latest output.
func TestConsoleLogIsCapped(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "console")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	c := &consoleLog{f: f, max: 1000}
	for i := 0; i < 500; i++ {
		if n, err := c.Write([]byte("0123456789")); n != 10 || err != nil {
			t.Fatalf("write = %d, %v", n, err)
		}
	}
	if n, err := c.Write([]byte(strings.Repeat("y", 5000) + "LAST")); n != 5004 || err != nil {
		t.Fatalf("oversized write = %d, %v", n, err)
	}
	info, err := f.Stat()
	if err != nil || info.Size() > 1000 {
		t.Fatalf("console log is %d bytes, cap 1000", info.Size())
	}
	body, _ := os.ReadFile(f.Name())
	if !strings.HasSuffix(string(body), "LAST") {
		t.Fatalf("the log lost the latest output: %q", body[max(0, len(body)-10):])
	}
}
