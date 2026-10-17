//go:build linux && metal

package fcvm

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
)

func TestMetalRehearsalSuspendResume(t *testing.T) {
	if os.Getenv("FAAS_TEST_REHEARSAL_SUSPEND_RESUME") != "1" {
		t.Skip("run rehearsal suspend/resume native acceptance")
	}
	kernel, _, _ := metalImages(t)
	base, layer := ensureV6Ext4(t, t.TempDir(), repoRoot(t))
	withCgroupRootAt(t, "/sys/fs/cgroup")
	p := &suspendWakePolicy{receipt: uuid.NewString(), state: "claimed", pauseMS: 1000}
	m := newMetalManager(t, kernel).WithLifecycleContext(t.Context()).WithRehearsalNetworkPolicies(p)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	req := WakeRequest{Instance: uuid.NewString(), AppID: uuid.NewString(), AccountID: uuid.NewString(), DeploymentID: uuid.NewString(), RehearsalRunID: rehearsalNetworkTestRun, Plan: "hobby", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 128}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		_ = m.Destroy(cleanup, req.Instance)
		leakcheck.AssertZero(t)
	})
	inst, err := m.Wake(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := m.InstanceRehearsalRecorder(req.Instance)
	if err != nil {
		t.Fatal(err)
	}
	p.onStep = func(next string) error {
		frozen, _ := m.FrozenState(req.Instance)
		if next == "paused" && !frozen {
			return errors.New("pause receipt preceded native pause")
		}
		if next == "resumed" && frozen {
			return errors.New("resume receipt preceded native resume")
		}
		return nil
	}
	if err = m.RehearseSuspendResume(ctx, req.Instance, p.receipt); err != nil {
		t.Fatal(err)
	}
	again, err := m.InstanceRehearsalRecorder(req.Instance)
	if err != nil || again != owner || p.state != "resumed" || p.starts != 1 || m.LiveCount() != 1 || m.LeasedCount() != 1 {
		t.Fatal("native resume replaced the original collector or VM")
	}
	if inst.Lease.Instance != req.Instance || inst.Frozen {
		t.Fatal("native lifecycle did not resume in place")
	}
	if err = m.Destroy(ctx, req.Instance); err != nil {
		t.Fatal(err)
	}
	leakcheck.AssertZero(t)
}
