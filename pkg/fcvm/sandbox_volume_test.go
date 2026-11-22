package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// adr: 20261122132055 §4 — a persistent session's volume is one sparse ext4
// file per session under the node's volume root, made once, grown offline
// and never shrunk.

type volumeRuns struct {
	calls []string
	fs    int64 // the filesystem size dumpe2fs reports
}

func (r *volumeRuns) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name)
	switch name {
	case "dumpe2fs":
		return []byte("Block count:              " + strconv.FormatInt(r.fs/4096, 10) + "\nBlock size:               4096\n"), nil
	case "resize2fs":
		info, err := os.Stat(args[len(args)-1])
		if err != nil {
			return nil, err
		}
		r.fs = info.Size()
	case "mkfs.ext4":
		info, err := os.Stat(args[len(args)-1])
		if err != nil {
			return nil, err
		}
		r.fs = info.Size()
	}
	return nil, nil
}

func TestSandboxVolumesEnsureCreatesGrowsAndNeverShrinks(t *testing.T) {
	runs := &volumeRuns{}
	v := NewSandboxVolumes(t.TempDir())
	v.run = runs.run
	ctx := context.Background()
	const account, sandbox = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"

	path, created, err := v.Ensure(ctx, account, sandbox, 2)
	if err != nil || !created || path != filepath.Join(v.Root, account, sandbox+".ext4") {
		t.Fatalf("create = %q %v %v", path, created, err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 2<<30 {
		t.Fatalf("volume %v %v", info, err)
	}
	if _, err := os.Stat(path + ".new"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the temporary volume was left: %v", err)
	}
	// The same size again does nothing.
	runs.calls = nil
	if _, created, err := v.Ensure(ctx, account, sandbox, 2); err != nil || created || strings.Join(runs.calls, ",") != "dumpe2fs" {
		t.Fatalf("ensure again = %v %v, ran %v", created, err, runs.calls)
	}
	// A larger size grows the file and the filesystem.
	runs.calls = nil
	if _, created, err := v.Ensure(ctx, account, sandbox, 5); err != nil || created || strings.Join(runs.calls, ",") != "dumpe2fs,e2fsck,resize2fs" {
		t.Fatalf("grow = %v %v, ran %v", created, err, runs.calls)
	}
	if info, _ := os.Stat(path); info.Size() != 5<<30 || runs.fs != 5<<30 {
		t.Fatalf("grown to %d, filesystem %d", info.Size(), runs.fs)
	}
	// A file that grew without its filesystem (a crash between the two)
	// is finished.
	runs.fs = 2 << 30
	runs.calls = nil
	if _, _, err := v.Ensure(ctx, account, sandbox, 5); err != nil || runs.fs != 5<<30 {
		t.Fatalf("finish grow = %v, filesystem %d, ran %v", err, runs.fs, runs.calls)
	}
	if _, _, err := v.Ensure(ctx, account, sandbox, 3); !errors.Is(err, ErrSandboxVolumeShrink) {
		t.Fatalf("shrink = %v", err)
	}
	if err := v.Remove(account, sandbox); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed volume still there: %v", err)
	}
}

func TestSandboxVolumesRefuseBadInput(t *testing.T) {
	v := NewSandboxVolumes(t.TempDir())
	v.run = (&volumeRuns{}).run
	ctx := context.Background()
	for _, tc := range []struct {
		account, sandbox string
		gb               int
	}{
		{"../etc", "s", 1},
		{"a", "x/y", 1},
		{"", "s", 1},
		{"a", "s", 0},
		{"a", "s", SandboxVolumeMaxGB + 1},
	} {
		if _, _, err := v.Ensure(ctx, tc.account, tc.sandbox, tc.gb); err == nil {
			t.Fatalf("Ensure(%q, %q, %d) succeeded", tc.account, tc.sandbox, tc.gb)
		}
	}
	if _, _, err := (*SandboxVolumes)(nil).Ensure(ctx, "a", "s", 1); err == nil {
		t.Fatal("an unconfigured node made a volume")
	}
}

func TestSandboxVolumeMkfsLabelsAndOwnsTheVolume(t *testing.T) {
	args := strings.Join(sandboxVolumeMkfsArgs("/v.ext4"), " ")
	for _, want := range []string{"-L " + SandboxVolumeLabel, "root_owner=1000:1000", "/v.ext4"} {
		if !strings.Contains(args, want) {
			t.Fatalf("mkfs args %q lack %q", args, want)
		}
	}
}

func TestColdBootConfigAttachesTheSandboxVolumeAsDrive2(t *testing.T) {
	spec := ColdBootSpec{KernelKey: "k", BaseKey: "b", LayerKey: "l", Tap: "tap0", VcpuCount: 1, MemSizeMiB: 512,
		SandboxVolume: "/srv/fc/sandbox-volumes/a/s.ext4", SkipReady: true}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg := BuildColdBootConfig(spec, 0)
	if len(cfg.Drives) != 3 || cfg.Drives[2].DriveID != DriveSandboxVolume || cfg.Drives[2].IsReadOnly ||
		cfg.Drives[2].PathOnHost != spec.SandboxVolume {
		t.Fatalf("drives = %+v", cfg.Drives)
	}
	spec.BuildKitSeedDrive = "/seed.ext4"
	if err := spec.Validate(); err == nil {
		t.Fatal("a sandbox volume beside a buildkit seed drive was accepted")
	}
}
