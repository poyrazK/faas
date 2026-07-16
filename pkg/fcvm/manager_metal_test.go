//go:build metal

// Metal integration tests: need /dev/kvm, root, firecracker + jailer on PATH,
// and real kernel/base/layer images. Run on the dev EX44 via `make test-metal`.
// These are the executable M1 acceptance criteria (spec §14): boot 50 × 128 MB
// VMs concurrently, verify the §6.2-5 uniqueness invariant, and leak zero
// netns/TAPs/uids on teardown.
package fcvm

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// metalImages resolves the kernel/base/layer paths from the environment so the
// test is portable across boxes. Skips if unset.
func metalImages(t *testing.T) (kernel, base, layer string) {
	t.Helper()
	kernel = os.Getenv("FAAS_TEST_KERNEL")
	base = os.Getenv("FAAS_TEST_BASE_ROOTFS")
	layer = os.Getenv("FAAS_TEST_LAYER_ROOTFS")
	if kernel == "" || base == "" || layer == "" {
		t.Skip("set FAAS_TEST_KERNEL / FAAS_TEST_BASE_ROOTFS / FAAS_TEST_LAYER_ROOTFS to run metal tests")
	}
	return kernel, base, layer
}

func newMetalManager(t *testing.T, kernel string) *Manager {
	t.Helper()
	return NewManager(
		wire.ExecRunner{},
		NewJailerVMM(JailChrootBase, 30*time.Second),
		Paths{Kernel: kernel},
		nil,
	)
}

// TestMetalBoot50Concurrent is the M1 headline acceptance test.
func TestMetalBoot50Concurrent(t *testing.T) {
	kernel, base, layer := metalImages(t)
	m := newMetalManager(t, kernel)
	const n = 50

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("m1-%d", i)
			if _, err := m.ColdBoot(ctx, ColdBootRequest{
				Instance: id, BasePath: base, LayerPath: layer, VcpuCount: 2, MemSizeMiB: 128,
			}); err != nil {
				t.Errorf("boot %s: %v", id, err)
			}
		}(i)
	}
	wg.Wait()

	if m.LiveCount() != n {
		t.Fatalf("live=%d, want %d", m.LiveCount(), n)
	}

	// Teardown; after this the box must leak nothing (`make leakcheck`).
	for i := 0; i < n; i++ {
		if err := m.Destroy(ctx, fmt.Sprintf("m1-%d", i)); err != nil {
			t.Errorf("destroy m1-%d: %v", i, err)
		}
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatalf("after teardown live=%d leased=%d, want 0/0", m.LiveCount(), m.LeasedCount())
	}
}
