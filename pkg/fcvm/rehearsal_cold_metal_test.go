//go:build linux && metal

package fcvm

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"os"
	"testing"
	"time"
)

func TestMetalRehearsalColdStart(t *testing.T) {
	if os.Getenv("FAAS_TEST_REHEARSAL_COLD_START") != "1" {
		t.Skip("run rehearsal-cold-start native acceptance")
	}
	kernel, _, _ := metalImages(t)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	image := ensureEgressGuestExt4WithScript(t, t.TempDir(), `#!/bin/sh
busybox mount -t tmpfs tmpfs /tmp
busybox mkdir -p /tmp/www
printf 'cold ready' > /tmp/www/index.html
exec busybox httpd -f -p 8080 -h /tmp/www
`)
	p := &coldWakePolicy{preparation: uuid.NewString()}
	m := newMetalManager(t, kernel).WithLifecycleContext(t.Context()).WithRehearsalNetworkPolicies(p)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	req := WakeRequest{Instance: uuid.NewString(), AppID: uuid.NewString(), AccountID: uuid.NewString(), DeploymentID: uuid.NewString(), RehearsalRunID: rehearsalNetworkTestRun, Plan: "hobby", BaseKey: image, LayerKey: image, VcpuCount: 2, MemSizeMiB: 128}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		_ = m.Destroy(cleanup, req.Instance)
		leakcheck.AssertZero(t)
	})
	if _, err := m.Wake(ctx, req); err != nil {
		t.Fatal(err)
	}
	p.onComplete = func() error {
		if m.LiveCount() != 0 || m.LeasedCount() != 0 {
			return errors.New("retirement receipt preceded physical cleanup")
		}
		return nil
	}
	if err := m.RetireRehearsalCold(ctx, req.Instance, p.preparation); err != nil {
		t.Fatal(err)
	}
	leakcheck.AssertZero(t)
	previous := req.Instance
	req.Instance = uuid.NewString()
	p.mu.Lock()
	p.required = true
	p.mu.Unlock()
	vm, err := m.Wake(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	methods := append([]string{}, p.methods...)
	complete := p.completed
	p.mu.Unlock()
	if !complete || vm.Lease.Instance == previous || vm.Method != WakeColdBoot || len(methods) != 2 || methods[1] != "cold_boot" {
		t.Fatal("fresh boot and original retirement were not proved")
	}
	if err = m.Destroy(ctx, req.Instance); err != nil {
		t.Fatal(err)
	}
	leakcheck.AssertZero(t)
}
