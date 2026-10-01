package fcvm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type restoreExitAttemptVMM struct {
	*fakeVMM
	manager           *Manager
	restoreGeneration uint64
	bootGeneration    uint64
	currentDies       bool
}

func (v *restoreExitAttemptVMM) Restore(ctx context.Context, lease Lease, spec RestoreSpec) error {
	v.restoreGeneration = lease.processGeneration
	err := v.fakeVMM.Restore(ctx, lease, spec)
	v.manager.ProcessExitedAttempt(lease.Instance, lease.processGeneration, 1)
	return err
}

func (v *restoreExitAttemptVMM) BootColdBoot(ctx context.Context, lease Lease, spec ColdBootSpec) error {
	v.bootGeneration = lease.processGeneration
	if err := v.fakeVMM.BootColdBoot(ctx, lease, spec); err != nil {
		return err
	}
	// The retired restore's watchdog can deliver after replacement startup.
	v.manager.ProcessExitedAttempt(lease.Instance, v.restoreGeneration, 1)
	if v.currentDies {
		v.manager.ProcessExitedAttempt(lease.Instance, lease.processGeneration, 137)
	}
	return nil
}

func TestWakeRestoreExitDoesNotRejectHealthyColdBoot(t *testing.T) {
	for _, currentDies := range []bool{false, true} {
		name := "healthy-replacement"
		if currentDies {
			name = "replacement-exited"
		}
		t.Run(name, func(t *testing.T) {
			vmm := &restoreExitAttemptVMM{
				fakeVMM:     &fakeVMM{restoreErr: errors.New("invalid snapshot state")},
				currentDies: currentDies,
			}
			manager := newTestManager(&fakeRunner{}, vmm)
			vmm.manager = manager
			var relayed int
			manager.WithLivenessSink(func(context.Context, string, string) { relayed++ })
			instance, err := manager.Wake(context.Background(), wakeReq(name, usableSnapshot()))
			if vmm.restoreGeneration == 0 || vmm.bootGeneration <= vmm.restoreGeneration {
				t.Fatalf("process attempts not distinct: restore=%d boot=%d", vmm.restoreGeneration, vmm.bootGeneration)
			}
			if currentDies {
				if err == nil || !strings.Contains(err.Error(), "exit code 137") {
					t.Fatalf("replacement exit was lost: %v", err)
				}
				if manager.LiveCount() != 0 || manager.LeasedCount() != 0 {
					t.Fatal("failed replacement retained VM resources")
				}
				return
			}
			if err != nil || instance.Method != WakeColdBoot {
				t.Fatalf("healthy fallback rejected: instance=%+v err=%v", instance, err)
			}
			t.Cleanup(func() { _ = manager.Destroy(context.Background(), name) })
			manager.ProcessExitedAttempt(name, vmm.restoreGeneration, 1)
			if relayed != 0 {
				t.Fatal("retired restore exit affected the published replacement")
			}
			manager.ProcessExitedAttempt(name, vmm.bootGeneration, 137)
			if relayed != 1 {
				t.Fatal("current process exit did not reach the lifecycle owner")
			}
		})
	}
}
