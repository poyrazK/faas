// adr: 097 — schedd's work before the vmmd RPC is split into phases, so the
// admission insert is measured apart from the reads around it.
package sched_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
)

const preRPCAdmitDelay = 200 * time.Millisecond

// slowAdmissionStore makes the instance admission insert slow, as the
// instances triggers made it on production-us (~38 ms on-CPU per wake).
type slowAdmissionStore struct {
	MemStore *state.MemStore
}

func (s *slowAdmissionStore) CreateInstanceWithMode(ctx context.Context, appID, deploymentID, current string, ramMB int, nodeID, wakeID, mode string) (state.Instance, error) {
	time.Sleep(preRPCAdmitDelay)
	return s.MemStore.CreateInstanceWithMode(ctx, appID, deploymentID, current, ramMB, nodeID, wakeID, mode)
}

func TestWakePreRPCPhasesAttributeAdmission(t *testing.T) {
	for _, restore := range []bool{false, true} {
		name := "cold"
		if restore {
			name = "restore"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := statetest.NewMem(&slowAdmissionStore{MemStore: state.NewMemStore()})
			_, app, dep := sched.SeedApp(t, store, api.PlanPro, 512, 5)
			if restore {
				if _, err := store.CreateSnapshot(ctx, state.Snapshot{
					DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: int64(app.RAMMB) << 20,
					StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "pre-rpc-timing"),
				}); err != nil {
					t.Fatal(err)
				}
			}
			ops := wire.NewOpsMetrics("schedd")
			e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
			e.WithEvents(events.NewPlatform("schedd", store, sched.TestLog(), nil, nil))
			if _, err := e.Wake(ctx, app.ID, "", "", ""); err != nil {
				t.Fatal(err)
			}

			floor := preRPCAdmitDelay.Seconds() * 0.75
			for _, phase := range []string{"before_admission", "lock_wait", "reads", "admit", "post_admit"} {
				if count := sched.ReadWakeRPC(t, ops, app.ID, phase, "count"); count != 1 {
					t.Fatalf("phase %s count = %v, want 1", phase, count)
				}
				sum := sched.ReadWakeRPC(t, ops, app.ID, phase, "sum")
				if phase == "admit" && sum < floor {
					t.Errorf("admit = %fs omits the %s admission insert", sum, preRPCAdmitDelay)
				}
				if phase != "admit" && sum >= floor {
					t.Errorf("%s = %fs includes the admission insert", phase, sum)
				}
			}
			if got := bootStartedField(t, store, "pre_rpc_admit_ms"); got < preRPCAdmitDelay.Milliseconds()*3/4 {
				t.Errorf("wake.boot_started pre_rpc_admit_ms = %d, want at least %d", got, preRPCAdmitDelay.Milliseconds()*3/4)
			}
		})
	}
}

// bootStartedField waits for the asynchronously emitted wake.boot_started row
// and returns one of its integer fields.
func bootStartedField(t *testing.T, store state.Store, key string) int64 {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, err := store.ListEvents(context.Background(), "", 0)
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		for _, row := range rows {
			if row.Kind != events.WakeBootStarted {
				continue
			}
			var data map[string]any
			if err := json.Unmarshal(row.Data, &data); err != nil {
				t.Fatalf("decode boot_started: %v", err)
			}
			v, ok := data[key].(float64)
			if !ok {
				t.Fatalf("wake.boot_started has no %s: %v", key, data)
			}
			return int64(v)
		}
		if time.Now().After(deadline) {
			t.Fatal("wake emitted no wake.boot_started event")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
