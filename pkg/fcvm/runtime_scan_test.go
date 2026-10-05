// adr: 590
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/runtimescan"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
)

func TestRuntimeScanReservesAllMountsBeforeStorageAndReleasesPartialReservation(t *testing.T) {
	for _, capacity := range []int{1, 2} {
		t.Run(string(rune('0'+capacity)), func(t *testing.T) {
			registry := vmmdmount.NewRegistry(capacity)
			backend := &parentMountAdmissionStorage{}
			m := &Manager{storage: backend, parentMounts: registry}
			r := runtimescan.Request{Version: 1, InputHash: strings.Repeat("a", 64), TargetDir: vmmdmount.OverlayStagingRoot + "/" + vmmdmount.RuntimeScanTargetPrefix + "capacity", Sources: []runtimeadmission.ArtifactSource{
				runtimeSourceFixture("base-image", "", "base/test.ext4", []byte("base")), runtimeSourceFixture("app-layer", "", "apps/test.ext4", []byte("main")),
			}}
			actual, err := m.MaterializeRuntimeScan(t.Context(), r)
			if !errors.Is(err, vmmdmount.ErrMountCapacity) || backend.gets != 0 || actual.Version != 0 {
				t.Fatal("capacity exhaustion crossed native byte boundary", err)
			}
			var leases []*vmmdmount.MountLease
			for range capacity {
				lease, err := registry.ReserveMount(t.Context())
				if err != nil {
					t.Fatal("partial reservation leaked", err)
				}
				leases = append(leases, lease)
			}
			for _, lease := range leases {
				if err := lease.Release(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestSourceRuntimeScanReservesOverlayBeforeReadingBytes(t *testing.T) {
	for _, kind := range []string{"source-app-layer", "function-layer"} {
		t.Run(kind, func(t *testing.T) {
			registry := vmmdmount.NewRegistry(2)
			backend := &parentMountAdmissionStorage{}
			m := &Manager{storage: backend, parentMounts: registry}
			r := runtimescan.Request{Version: 1, InputHash: strings.Repeat("a", 64), TargetDir: vmmdmount.OverlayStagingRoot + "/" + vmmdmount.RuntimeScanTargetPrefix + "source-capacity", Sources: []runtimeadmission.ArtifactSource{
				runtimeSourceFixture("base-image", "", "base/test.ext4", []byte("base")), runtimeSourceFixture(kind, "", "apps/source.ext4", []byte("main")),
			}}
			actual, err := m.MaterializeRuntimeScan(t.Context(), r)
			if !errors.Is(err, vmmdmount.ErrMountCapacity) || backend.gets != 0 || actual.Version != 0 {
				t.Fatal("source overlay omitted mount reservation", err)
			}
			for range 2 {
				lease, err := registry.ReserveMount(t.Context())
				if err != nil {
					t.Fatal("partial source reservation leaked", err)
				}
				if err := lease.Release(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

type fixtureRuntimeProjectionTarget struct{ root *os.Root }

func (f fixtureRuntimeProjectionTarget) CreateView(name string) (*os.Root, error) {
	if err := f.root.Mkdir(name, 0700); err != nil {
		return nil, err
	}
	return f.root.OpenRoot(name)
}

func TestRuntimeScanProjectsSeparateFullAndSidecarGuestRoots(t *testing.T) {
	main, sidecar, target := t.TempDir(), t.TempDir(), t.TempDir()
	for _, root := range []string{main, sidecar} {
		if err := os.Mkdir(filepath.Join(root, "etc"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "etc", "package"), []byte(root), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/etc/package", filepath.Join(root, "absolute")); err != nil {
			t.Fatal(err)
		}
	}
	r := runtimescan.Request{Version: 1, InputHash: strings.Repeat("a", 64), TargetDir: target, Sources: []runtimeadmission.ArtifactSource{
		runtimeSourceFixture("base-image", "", "base/test.ext4", []byte("base")), runtimeSourceFixture("full-rootfs", "", "apps/test.ext4", []byte("main")), runtimeSourceFixture("sidecar-layer", "metrics", "sidecar/metrics.ext4", []byte("sidecar")),
	}}
	pinned, err := os.OpenRoot(target)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	receipt, err := projectRuntimeScanRoots(t.Context(), r, map[string]string{"": main, "metrics": sidecar}, fixtureRuntimeProjectionTarget{pinned})
	if err != nil || receipt.Check(r) != nil {
		t.Fatal("separate guest-root projections lost identity", err)
	}
	for _, view := range receipt.Views {
		root := main
		if view.WorkloadName != "" {
			root = sidecar
		}
		if data, err := os.ReadFile(filepath.Join(target, runtimescan.ViewDirectory(view.WorkloadName), "absolute")); err != nil || string(data) != root {
			t.Fatal("sidecar/main link resolved across guest roots", err)
		}
	}
}

func TestRuntimeScanFullRootfsMarkerCannotSelectUnverifiedLayout(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "wrong", "symlink", "directory", "unexpected"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			name := filepath.Join(root, strings.TrimPrefix(api.FullRootfsMarkerPath, "/"))
			if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "valid", "unexpected":
				if err := os.WriteFile(name, []byte(api.FullRootfsMarkerValue), 0444); err != nil {
					t.Fatal(err)
				}
			case "wrong":
				if err := os.WriteFile(name, []byte("other"), 0444); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.WriteFile(filepath.Join(root, "payload"), []byte(api.FullRootfsMarkerValue), 0444); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../../payload", name); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(name, 0755); err != nil {
					t.Fatal(err)
				}
			}
			err := checkRuntimeScanFullRootfsMarker(root, mode != "unexpected")
			if (err == nil) != (mode == "valid") {
				t.Fatal("unsupported marker acquired layout authority", mode, err)
			}
		})
	}
}

func TestRuntimeScanCancellationClearsReceiptEvenAfterProjection(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	r := runtimescan.Receipt{Version: 1}
	var err error
	finishRuntimeScan(ctx, nil, nil, &r, &err)
	if !errors.Is(err, context.Canceled) || r.Version != 0 {
		t.Fatal("canceled operation retained a receipt")
	}
	if checkRuntimeScanBudget(api.ApplicationStandardRuntimeScanMaxBytes, 0, 1, 1) == nil || checkRuntimeScanBudget(0, api.ApplicationStandardRuntimeScanMaxEntries, 0, 1) == nil {
		t.Fatal("aggregate scan budget was not enforced")
	}
}
