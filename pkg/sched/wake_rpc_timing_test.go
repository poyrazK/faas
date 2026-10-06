package sched

// adr: 097 — publication waits belong to rpc_to_running, not rpc_call.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

const wakeTimingPostRPCDelay = 200 * time.Millisecond

type wakeTimingStore struct {
	*state.MemStore
	boostWrites int
}

func (s *wakeTimingStore) SetInstanceStartupCPUBoostUntil(ctx context.Context, id string, until *time.Time) error {
	s.boostWrites++
	if s.boostWrites == 2 {
		// First write reserves the provisional boost before boot. The second
		// persists its tail after vmmd returns, before RUNNING publication.
		time.Sleep(wakeTimingPostRPCDelay)
	}
	return s.MemStore.SetInstanceStartupCPUBoostUntil(ctx, id, until)
}

func seedWakeTiming(t *testing.T, store state.Store, vmm *fakeVMM, restore bool, cpu int) (*Engine, *wire.OpsMetrics, state.App) {
	t.Helper()
	ctx := context.Background()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{CPUMillicores: &cpu}); err != nil {
		t.Fatal(err)
	}
	if restore {
		if _, err := store.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: int64(app.RAMMB) << 20,
			StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "rpc-timing"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	ops := wire.NewOpsMetrics("schedd")
	return newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops), ops, app
}

func assertPostRPCPhase(t *testing.T, ops *wire.OpsMetrics, appID string) {
	t.Helper()
	for _, phase := range []string{"admit_to_rpc", "rpc_call", "rpc_to_running"} {
		if count := readWakeRPC(t, ops, appID, phase, "count"); count != 1 {
			t.Fatalf("phase %s count=%v", phase, count)
		}
	}
	if got := readWakeRPC(t, ops, appID, "rpc_to_running", "sum"); got < wakeTimingPostRPCDelay.Seconds()*0.75 {
		t.Errorf("rpc_to_running=%fs omits the %s post-RPC wait", got, wakeTimingPostRPCDelay)
	}
	if got := readWakeRPC(t, ops, appID, "rpc_call", "sum"); got >= wakeTimingPostRPCDelay.Seconds()*0.75 {
		t.Errorf("rpc_call=%fs includes post-RPC work", got)
	}
}

func TestWakeRPCPhasesSeparatePostRPCStoreDelay(t *testing.T) {
	for _, restore := range []bool{false, true} {
		name := "cold"
		if restore {
			name = "restore"
		}
		t.Run(name, func(t *testing.T) {
			store := &wakeTimingStore{MemStore: state.NewMemStore()}
			e, ops, app := seedWakeTiming(t, store, &fakeVMM{}, restore, 250)
			if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err != nil {
				t.Fatal(err)
			}
			if store.boostWrites != 2 {
				t.Fatalf("boost writes=%d, want 2", store.boostWrites)
			}
			assertPostRPCPhase(t, ops, app.ID)
		})
	}
}

func TestWakeRPCPhasesSeparatePublicationLockWait(t *testing.T) {
	for _, restore := range []bool{false, true} {
		name := "cold"
		if restore {
			name = "restore"
		}
		t.Run(name, func(t *testing.T) {
			vmm := &fakeVMM{bootStarted: make(chan struct{}, 1), bootRelease: make(chan struct{})}
			e, ops, app := seedWakeTiming(t, state.NewMemStore(), vmm, restore, api.DefaultAppCPUMillicores)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := e.Wake(ctx, app.ID, "", "", ""); done <- err }()
			select {
			case <-vmm.bootStarted:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			unlock := e.lockApp(app.ID)
			close(vmm.bootRelease)
			time.Sleep(wakeTimingPostRPCDelay)
			unlock()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			assertPostRPCPhase(t, ops, app.ID)
		})
	}
}
