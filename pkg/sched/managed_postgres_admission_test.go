// adr: 466 — cutover barriers apply even to scheduler paths bypassing request gates.
package sched

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type cutoverFencedStore struct {
	*state.MemStore
	readErr error
}

func (s *cutoverFencedStore) ManagedPostgresAdmissionFenced(context.Context, string) (bool, error) {
	return true, s.readErr
}

func TestManagedPostgresAdmissionBlocksSchedulerPaths(t *testing.T) {
	for _, lookupErr := range []error{nil, errors.New("catalog unavailable")} {
		t.Run(cutoverAdmissionErrorName(lookupErr), func(t *testing.T) {
			store := &cutoverFencedStore{MemStore: state.NewMemStore(), readErr: lookupErr}
			_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
			ctx := context.Background()
			// Wake's fast path must not reuse an existing running writer.
			if _, err := store.CreateInstance(ctx, app.ID, dep.ID, "running", 256, state.DefaultLocalNodeName, ""); err != nil {
				t.Fatal(err)
			}
			vmm := &fakeVMM{}
			e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			want := state.ErrManagedPostgresAdmissionFenced
			if lookupErr != nil {
				want = lookupErr
			}
			calls := []func() error{
				func() error { _, err := e.Wake(ctx, app.ID, dep.ID, "default", "cron"); return err },
				func() error {
					_, err := e.admitAndDispatchWithOptions(ctx, app.ID, dep.ID, "normal", TriggerRuntimeConfigRestart, true, true)
					return err
				},
				func() error {
					_, err := e.admitAndDispatchWithOptions(ctx, app.ID, dep.ID, "worker", "worker", true, true)
					return err
				},
				func() error {
					_, err := e.admitAndDispatchWithOptions(ctx, app.ID, dep.ID, "job", "job", true, true)
					return err
				},
				func() error {
					_, err := e.admitAndDispatchWithOptions(ctx, app.ID, dep.ID, "mirror", "mirror", true, true)
					return err
				},
			}
			for i, call := range calls {
				if err := call(); !errors.Is(err, want) {
					t.Fatalf("path %d: %v", i, err)
				}
			}
			if vmm.coldBoots != 0 || vmm.restores != 0 {
				t.Fatal("fenced scheduler invoked vmmd")
			}
		})
	}
}

func cutoverAdmissionErrorName(err error) string {
	if err != nil {
		return "read_failure"
	}
	return "fenced"
}

type cutoverAdmissionRaceStore struct {
	*state.MemStore
	fenced atomic.Bool
}

func (s *cutoverAdmissionRaceStore) ManagedPostgresAdmissionFenced(context.Context, string) (bool, error) {
	return s.fenced.Load(), nil
}

func (s *cutoverAdmissionRaceStore) PublishOwnedInstanceRuntime(ctx context.Context, publication state.RuntimeInstancePublication) (state.Instance, error) {
	if s.fenced.Load() {
		return state.Instance{}, state.ErrManagedPostgresAdmissionFenced
	}
	return s.MemStore.PublishOwnedInstanceRuntime(ctx, publication)
}

func TestManagedPostgresAdmissionFenceDuringBootDestroysCandidate(t *testing.T) {
	store := &cutoverAdmissionRaceStore{MemStore: state.NewMemStore()}
	_, app, _ := seedApp(t, store, api.PlanPro, 256, 5)
	vmm := &fakeVMM{coldBootHook: func() { store.fenced.Store(true) }}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.Wake(context.Background(), app.ID, "", "", ""); !errors.Is(err, state.ErrManagedPostgresAdmissionFenced) {
		t.Fatalf("boot raced the fence: %v", err)
	}
	if vmm.coldBoots != 1 || vmm.destroys != 1 || e.Ledger().Concurrency(app.ID) != 0 {
		t.Fatalf("cold=%d destroy=%d concurrency=%d", vmm.coldBoots, vmm.destroys, e.Ledger().Concurrency(app.ID))
	}
	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range instances {
		if instance.State == "running" {
			t.Fatal("racing boot was published as running")
		}
	}
}
