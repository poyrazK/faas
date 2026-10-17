//go:build linux && metal

// adr: 1522
package fcvm

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestMetalOperatorRestartGeneration(t *testing.T) {
	kernel, base, layer := metalImages(t)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	vmm := newMetalVMM(t, 30*time.Second)
	manager := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil)
	request := admissionWake("operator-restart-generation")
	request.BaseKey, request.LayerKey = base, layer
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := manager.Destroy(cleanupCtx, request.Instance); err != nil {
			t.Error(err)
		}
	})
	if _, err := manager.Wake(ctx, request); err != nil {
		t.Fatal(err)
	}
	generation, err := manager.OperatorRestartGeneration(request.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.DestroyOperatorRestart(ctx, request.Instance, generation); err != nil {
		t.Fatal(err)
	}
	if manager.LiveCount() != 0 || manager.LeasedCount() != 0 {
		t.Fatal("acknowledged incomplete teardown")
	}
	if _, err := manager.Wake(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := manager.DestroyOperatorRestart(ctx, request.Instance, generation); err == nil {
		t.Fatal("old operator action reached replacement VM")
	}
	if manager.LiveCount() != 1 {
		t.Fatal("replacement VM disappeared")
	}
	if err := manager.Destroy(ctx, request.Instance); err != nil {
		t.Fatal(err)
	}
	leakcheck.AssertZero(t)
}
