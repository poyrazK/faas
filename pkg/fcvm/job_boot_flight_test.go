package fcvm

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// An artifact restore can ignore cancellation until an I/O call returns. The
// manager must still fence its late successful return, and a concurrent stop
// must wait until the lease/netns/VMM cleanup has finished.
type lateJobBootVMM struct {
	*fakeVMM
	entered    chan struct{}
	cancelSeen chan struct{}
	release    chan struct{}
}

func (v *lateJobBootVMM) BootColdBootForJob(ctx context.Context, _ Lease, spec JobColdBootSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	close(v.entered)
	<-ctx.Done()
	close(v.cancelSeen)
	<-v.release
	return nil // Simulate a late VMM success after cancellation.
}

func testJobBootRequest(instance string) JobBootRequest {
	return JobBootRequest{
		Instance:       instance,
		AccountID:      "account-1",
		Plan:           api.PlanHobby,
		RunID:          "run-1",
		ImageRef:       "jobs/550e8400-e29b-41d4-a716-446655440000.ext4",
		KernelKey:      "kernel/v1.10.0",
		BaseKey:        "base/runner-node22.ext4",
		Command:        []string{"/bin/true"},
		VcpuCount:      1,
		MemSizeMiB:     128,
		TaskTimeoutSec: 60,
		LeaseToken:     "lease-1",
	}
}

func TestBootJobStopCancelsInFlightAndFencesLateSuccess(t *testing.T) {
	for _, tc := range []struct {
		name string
		stop func(context.Context, *Manager, string) error
	}{
		{name: "destroy", stop: func(ctx context.Context, m *Manager, id string) error {
			return m.Destroy(ctx, id)
		}},
		{name: "signal and kill", stop: func(ctx context.Context, m *Manager, id string) error {
			_, _, err := m.SignalAndKill(ctx, id, syscall.SIGTERM, time.Second)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			v := &lateJobBootVMM{
				fakeVMM:    &fakeVMM{},
				entered:    make(chan struct{}),
				cancelSeen: make(chan struct{}),
				release:    make(chan struct{}),
			}
			m := newTestManager(&fakeRunner{}, v)
			id := "job-boot-" + tc.name
			bootErr := make(chan error, 1)
			go func() {
				_, err := m.BootJob(ctx, testJobBootRequest(id))
				bootErr <- err
			}()
			select {
			case <-v.entered:
			case <-ctx.Done():
				t.Fatal("job boot did not enter VMM")
			}
			stopErr := make(chan error, 1)
			go func() { stopErr <- tc.stop(ctx, m, id) }()
			select {
			case <-v.cancelSeen:
			case <-ctx.Done():
				t.Fatal("stop did not cancel in-flight job boot")
			}
			select {
			case err := <-stopErr:
				t.Fatalf("stop returned before VMM unwound: %v", err)
			default:
			}
			close(v.release)
			select {
			case err := <-bootErr:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("late boot result = %v, want cancellation", err)
				}
			case <-ctx.Done():
				t.Fatal("job boot did not finish")
			}
			select {
			case err := <-stopErr:
				if err != nil {
					t.Fatalf("stop: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("stop did not finish")
			}
			if got := m.LiveCount(); got != 0 {
				t.Fatalf("live jobs = %d, want zero", got)
			}
			if got := m.alloc.InUse(); got != 0 {
				t.Fatalf("allocator leases = %d, want zero", got)
			}
			m.mu.Lock()
			flights := len(m.jobBoots)
			m.mu.Unlock()
			if flights != 0 {
				t.Fatalf("in-flight job boots = %d, want zero", flights)
			}
			v.mu.Lock()
			kills := len(v.killed)
			v.mu.Unlock()
			if kills == 0 {
				t.Fatal("canceled VMM boot was not killed")
			}
		})
	}
}
