// adr: 592
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type runtimeDriveFixture struct {
	vmm     *JailerVMM
	lease   Lease
	root    string
	config  VMConfig
	spec    ColdBootSpec
	sources []runtimeadmission.ArtifactSource
}

func newRuntimeDriveFixture(t *testing.T) runtimeDriveFixture {
	t.Helper()
	bodies := map[string][]byte{"base/a.ext4": []byte("base"), "rootfs/main.ext4": []byte("main")}
	return newRuntimeDriveFixtureWithBodies(t, bodies)
}

func newRuntimeDriveFixtureWithBodies(t *testing.T, bodies map[string][]byte) runtimeDriveFixture {
	t.Helper()
	sources := []runtimeadmission.ArtifactSource{runtimeSourceFixture("base-image", "", "base/a.ext4", bodies["base/a.ext4"]), runtimeSourceFixture("app-layer", "", "rootfs/main.ext4", bodies["rootfs/main.ext4"])}
	v := &JailerVMM{storage: &runtimeSourceTestBackend{data: bodies}}
	lease := Lease{Instance: idDead, UID: 20000, GID: 20000, Slot: 1}
	spec := ColdBootSpec{KernelKey: "kernel/vmlinux", BaseKey: sources[0].StorageKey, LayerKey: sources[1].StorageKey, VcpuCount: 2, MemSizeMiB: 128, Tap: "tap0"}
	if _, err := v.prepareVerifiedColdBoot(t.Context(), lease, spec, sources); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.releaseRuntimeSources(lease.Instance) })
	root := t.TempDir()
	config := BuildColdBootConfig(spec, lease.Slot)
	for i := range config.Drives {
		name := config.Drives[i].DriveID + ".ext4"
		if err := os.WriteFile(filepath.Join(root, name), bodies[config.Drives[i].PathOnHost], 0o600); err != nil {
			t.Fatal(err)
		}
		config.Drives[i].PathOnHost = name
	}
	return runtimeDriveFixture{vmm: v, lease: lease, root: root, config: config, spec: spec, sources: sources}
}

func (f runtimeDriveFixture) pin(t *testing.T) {
	t.Helper()
	if err := f.vmm.pinApprovedRuntimeDrives(t.Context(), f.lease, f.root, f.config); err != nil {
		t.Fatal(err)
	}
}

func (f runtimeDriveFixture) measure(t *testing.T) *runtimeDriveHandoff {
	t.Helper()
	body, err := json.Marshal(f.config)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.vmm.measureFinalRuntimeDrives(t.Context(), f.lease, f.root, body); err != nil {
		t.Fatal(err)
	}
	handoff, err := f.vmm.runtimeDriveHandoff(f.lease)
	if err != nil {
		t.Fatal(err)
	}
	return handoff
}

func TestRuntimeDriveHandoffKeepsProducerAndInjectedBytesDistinct(t *testing.T) {
	f := newRuntimeDriveFixture(t)
	f.pin(t)
	if err := os.WriteFile(filepath.Join(f.root, f.config.Drives[1].PathOnHost), []byte("env!"), 0o600); err != nil {
		t.Fatal(err)
	}
	handoff := f.measure(t)
	pinnedFile := handoff.drives[0].file
	base, main := handoff.drives[0].observation, handoff.drives[1].observation
	if base.Producer != base.Injected || main.Producer == main.Injected || main.Producer.Digest != f.sources[1].Digest || main.Injected.Bytes != main.Producer.Bytes {
		t.Fatal("producer lineage or injected identity was collapsed")
	}
	if _, err := f.vmm.ObservedRuntimeDrives(t.Context(), f.lease); err == nil {
		t.Fatal("staged facts fabricated a native process observation")
	}
	if err := f.vmm.releaseRuntimeSources(f.lease.Instance); err != nil {
		t.Fatal(err)
	}
	if len(f.vmm.runtimeDriveHandoffs) != 0 || handoff.drives != nil || !handoff.closed {
		t.Fatal("native observation ownership leaked after teardown")
	}
	if _, err := pinnedFile.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("teardown retained a pinned drive descriptor", err)
	}
}

func TestRuntimeDriveHandoffRejectsWritableAliasOfApprovedReadOnlySource(t *testing.T) {
	body := []byte("same approved bytes")
	f := newRuntimeDriveFixtureWithBodies(t, map[string][]byte{"base/a.ext4": body, "rootfs/main.ext4": body})
	main := filepath.Join(f.root, f.config.Drives[1].PathOnHost)
	if err := os.Remove(main); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(f.root, f.config.Drives[0].PathOnHost), main); err != nil {
		t.Fatal(err)
	}
	if err := f.vmm.pinApprovedRuntimeDrives(t.Context(), f.lease, f.root, f.config); !errors.Is(err, runtimeadmission.ErrInvalid) {
		t.Fatal("matching content allowed a writable drive to alias its shared base", err)
	}
}

func TestRuntimeDriveHandoffMapsSidecarSourcesToConfiguredDriveIDs(t *testing.T) {
	f := newRuntimeDriveFixture(t)
	if err := f.vmm.releaseRuntimeSources(f.lease.Instance); err != nil {
		t.Fatal(err)
	}
	backend := f.vmm.storage.(*runtimeSourceTestBackend)
	backend.data["sidecars/metrics.ext4"] = []byte("metrics")
	f.sources = append(f.sources, runtimeSourceFixture("sidecar-layer", "metrics", "sidecars/metrics.ext4", backend.data["sidecars/metrics.ext4"]))
	slices.Reverse(f.sources)
	f.spec.LayerKey = ""
	f.spec.Workloads = []WorkloadSpec{
		{Name: "main", Type: "main", StorageKey: "rootfs/main.ext4", DriveID: "custom-main"},
		{Name: "metrics", Type: "sidecar", StorageKey: "sidecars/metrics.ext4", DriveID: "custom-metrics"},
	}
	if _, err := f.vmm.prepareVerifiedColdBoot(t.Context(), f.lease, f.spec, f.sources); err != nil {
		t.Fatal(err)
	}
	f.config = BuildColdBootConfig(f.spec, f.lease.Slot)
	for i := range f.config.Drives {
		drive := &f.config.Drives[i]
		body, name := backend.data[drive.PathOnHost], drive.DriveID+".ext4"
		if err := os.WriteFile(filepath.Join(f.root, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
		drive.PathOnHost = name
	}
	f.pin(t)
	handoff := f.measure(t)
	if len(handoff.drives) != 3 || handoff.drives[1].observation.DriveID != "custom-main" || handoff.drives[1].observation.Source.Role() != "main" || handoff.drives[2].observation.DriveID != "custom-metrics" || handoff.drives[2].observation.Source.Role() != "sidecar:metrics" {
		t.Fatal("source ordering or custom drive IDs changed workload membership")
	}
}

func TestRuntimeDriveHandoffRejectsWrongStagedBytesAndPermissions(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*testing.T, runtimeDriveFixture)
	}{
		{"bytes", func(t *testing.T, f runtimeDriveFixture) { t.Helper(); mustWriteRuntimeDrive(t, f, 1, []byte("evil")) }},
		{"truncated", func(t *testing.T, f runtimeDriveFixture) { t.Helper(); mustWriteRuntimeDrive(t, f, 1, []byte("x")) }},
		{"base writable", func(t *testing.T, f runtimeDriveFixture) { t.Helper(); f.config.Drives[0].IsReadOnly = false }},
		{"main root", func(t *testing.T, f runtimeDriveFixture) { t.Helper(); f.config.Drives[1].IsRootDevice = true }},
		{"duplicate drive", func(t *testing.T, f runtimeDriveFixture) { t.Helper(); f.config.Drives[1].DriveID = DriveBase }},
		{"outside", func(t *testing.T, f runtimeDriveFixture) {
			t.Helper()
			f.config.Drives[1].PathOnHost = "../outside.ext4"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newRuntimeDriveFixture(t)
			test.edit(t, f)
			if err := f.vmm.pinApprovedRuntimeDrives(t.Context(), f.lease, f.root, f.config); err == nil {
				t.Fatal("unapproved staged drive accepted")
			}
		})
	}
}

func mustWriteRuntimeDrive(t *testing.T, f runtimeDriveFixture, index int, body []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, f.config.Drives[index].PathOnHost), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeDriveHandoffRejectsReadOnlyMutationAndPathReplacement(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(strconv.FormatBool(replace), func(t *testing.T) {
			f := newRuntimeDriveFixture(t)
			f.pin(t)
			if !replace {
				mustWriteRuntimeDrive(t, f, 0, []byte("evil"))
			} else {
				path := filepath.Join(f.root, f.config.Drives[1].PathOnHost)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				mustWriteRuntimeDrive(t, f, 1, []byte("main"))
			}
			body, _ := json.Marshal(f.config)
			if err := f.vmm.measureFinalRuntimeDrives(t.Context(), f.lease, f.root, body); err == nil {
				t.Fatal("changed read-only bytes or replaced inode accepted")
			}
		})
	}
}

func TestRuntimeDriveHandoffRejectsAnotherLeaseAndReplayKeepsOwner(t *testing.T) {
	f := newRuntimeDriveFixture(t)
	other := f.lease
	other.processGeneration++
	if err := f.vmm.pinApprovedRuntimeDrives(t.Context(), other, f.root, f.config); !errors.Is(err, runtimeadmission.ErrStale) {
		t.Fatal("another native attempt borrowed staged authority", err)
	}
	owner, _ := f.vmm.runtimeDriveHandoff(f.lease)
	if err := f.vmm.BootColdBootVerified(t.Context(), f.lease, f.spec, f.sources); !errors.Is(err, runtimeadmission.ErrReplay) {
		t.Fatal("replayed preparation accepted", err)
	}
	current, _ := f.vmm.runtimeDriveHandoff(f.lease)
	if current != owner || owner.closed || f.vmm.runtimeSources().root == "" {
		t.Fatal("rejected replay removed its original owner's sources")
	}
}

func runtimeDriveProcFixture(t *testing.T, handoff *runtimeDriveHandoff) string {
	t.Helper()
	root := t.TempDir()
	process := filepath.Join(root, "42")
	for _, dir := range []string{"fd", "fdinfo"} {
		if err := os.MkdirAll(filepath.Join(process, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fields := strings.Fields(strings.Repeat("0 ", 20))
	fields[0], fields[19] = "R", "101"
	for name, body := range map[string]string{"status": "Name:\tfirecracker\nUid:\t20000\t20000\t20000\t20000\n", "stat": "42 (firecracker worker) " + strings.Join(fields, " ")} {
		if err := os.WriteFile(filepath.Join(process, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i, drive := range handoff.drives {
		fd := strconv.Itoa(i + 3)
		if err := os.Symlink(drive.file.Name(), filepath.Join(process, "fd", fd)); err != nil {
			t.Fatal(err)
		}
		flags := "0100000"
		if !drive.observation.ReadOnly {
			flags = "0100002"
		}
		if err := os.WriteFile(filepath.Join(process, "fdinfo", fd), []byte("flags:\t"+flags+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRuntimeDriveObserverRequiresMeasuredNativeHandlesAndModes(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*testing.T, string)
	}{
		{"exact", func(*testing.T, string) {}},
		{"missing", func(t *testing.T, proc string) {
			t.Helper()
			if err := os.Remove(filepath.Join(proc, "42/fd/4")); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong inode", func(t *testing.T, proc string) {
			t.Helper()
			path := filepath.Join(proc, "42/fd/4")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("main"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"base writable", func(t *testing.T, proc string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(proc, "42/fdinfo/3"), []byte("flags:\t0100002\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"main readonly", func(t *testing.T, proc string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(proc, "42/fdinfo/4"), []byte("flags:\t0100000\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong uid", func(t *testing.T, proc string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(proc, "42/status"), []byte("Uid:\t20001\t20001\t20001\t20001\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newRuntimeDriveFixture(t)
			f.pin(t)
			handoff := f.measure(t)
			proc := runtimeDriveProcFixture(t, handoff)
			test.edit(t, proc)
			start, err := observeRuntimeDriveHandles(t.Context(), proc, 42, 20000, handoff.drives)
			if (err == nil) != (test.name == "exact") || test.name == "exact" && start != "101" {
				t.Fatalf("native handles start=%q err=%v", start, err)
			}
		})
	}
}

func TestRuntimeDriveHandoffCancellationCannotPublishObservation(t *testing.T) {
	f := newRuntimeDriveFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.vmm.pinApprovedRuntimeDrives(ctx, f.lease, f.root, f.config); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled producer measurement accepted", err)
	}
	if _, err := f.vmm.ObservedRuntimeDrives(ctx, f.lease); err == nil {
		t.Fatal("cancelled measurement published observation")
	}
}

func TestRuntimeDriveObserverRejectsAmbiguousOrNonIODescriptorFlags(t *testing.T) {
	for _, body := range []string{"", "flags:\t010000000\n", "flags:\t1\n", "flags:\t3\n", "flags:\t0\nflags:\t2\n", "flags:\tx\n", "flags:\t0 extra\n", strings.Repeat("x", 65537)} {
		t.Run(strconv.Itoa(len(body))+"-"+strconv.Itoa(strings.Count(body, "flags:")), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fdinfo")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := runtimeDriveDescriptorReadOnly(t.Context(), path); err == nil {
				t.Fatal("descriptor without a unique read/write mode was accepted")
			}
		})
	}
}
