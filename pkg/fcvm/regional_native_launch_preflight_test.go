//go:build linux || darwin

package fcvm

// adr: 630
import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
)

type nativeRegionalNoPreflightVMM struct {
	VMM
	nativeRecoveryVMM
	nativeRegionalLeaseVMM
}

type nativeRegionalNoLeaseVMM struct {
	VMM
	nativeRecoveryVMM
	nativeRegionalLaunchPreflighter
}

func TestRegionalNativeLaunchPreflightPrecedesHostRegistration(t *testing.T) {
	for _, scenario := range []string{"legacy", "incomplete", "missing-preflight", "missing-lease", "recovery-failure", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			m, v, host, req := nativeRegionalManagerFixture(t)
			ctx := t.Context()
			cause := errors.New("native inventory uncertain")
			switch scenario {
			case "legacy":
				m.vmm = NewJailerVMM(t.TempDir(), 0)
			case "incomplete":
				m.vmm = NewJailerVMM(t.TempDir(), 0)
				m.vmm.(*JailerVMM).nativeRecovery = &nativeProcessRecoveryRuntime{}
			case "missing-preflight":
				m.vmm = &nativeRegionalNoPreflightVMM{VMM: m.vmm, nativeRecoveryVMM: m.vmm.(nativeRecoveryVMM), nativeRegionalLeaseVMM: m.vmm.(nativeRegionalLeaseVMM)}
			case "missing-lease":
				m.vmm = &nativeRegionalNoLeaseVMM{VMM: m.vmm, nativeRecoveryVMM: m.vmm.(nativeRecoveryVMM), nativeRegionalLaunchPreflighter: m.vmm.(nativeRegionalLaunchPreflighter)}
			case "recovery-failure":
				v.nativeRecovery.support = func() error { return cause }
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := m.Wake(ctx, req)
			want := runtimequotahost.ErrStorage
			if scenario == "recovery-failure" {
				want = cause
			} else if scenario == "canceled" {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatal("unsupported producer did not fail at preflight", err)
			}
			rows, err := host.PermitInventory()
			if err != nil || len(rows) != 0 || m.LeasedCount() != 0 || m.LiveCount() != 0 || len(m.run.(*fakeRunner).commands) != 0 {
				t.Fatal("unsupported launch registered a physical hold or reached effects", err)
			}
		})
	}
}

func TestRegionalNativeLegacyPreflightDoesNotClaimPreparedNetwork(t *testing.T) {
	m, pool := testPreparedPool(t, 1)
	_, host, req := regionalHostFixture(t, &fakeVMM{})
	m.WithRuntimeHostJournal(host).WithRuntimeAuthorizer(func(context.Context, RuntimeAdmission) error { return nil })
	policy, ok := m.preparedPolicy(req)
	if !ok {
		t.Fatal("regional request is not cache eligible")
	}
	pool.observe(policy)
	pool.fill()
	if len(pool.ready) != 1 {
		t.Fatal("prepared network fixture did not fill")
	}
	before := pool.ready[0]
	commands := len(m.run.(*fakeRunner).commands)
	moves := 0
	pool.move = func(string, string) error { moves++; return nil }
	m.vmm = NewJailerVMM(t.TempDir(), 0)
	if _, err := m.Wake(t.Context(), req); !errors.Is(err, runtimequotahost.ErrStorage) {
		t.Fatal("legacy native mode accepted regional wake", err)
	}
	rows, err := host.PermitInventory()
	if err != nil || len(rows) != 0 || len(pool.ready) != 1 || !reflect.DeepEqual(pool.ready[0], before) || moves != 0 || len(m.alloc.reserved) != 1 || m.LeasedCount() != 0 || len(m.run.(*fakeRunner).commands) != commands {
		t.Fatal("unsupported wake touched the quota or prepared network", err)
	}
}

func TestRegionalNativePreflightKeepsPortableLaunchSurface(t *testing.T) {
	m, host, req := regionalHostFixture(t, &fakeVMM{})
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	rows, err := host.PermitInventory()
	if err != nil || len(rows) != 1 || rows[0].Permit != req.RegionalRuntime.Permit {
		t.Fatal("portable launch lost its original registration", err)
	}
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
	if err := m.preflightNativeRegionalRuntime(t.Context(), (*runtimequota.Placement)(nil)); err != nil {
		t.Fatal("independent launch needs no regional producer", err)
	}
}
