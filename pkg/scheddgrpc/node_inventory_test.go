// adr: 419 — resource telemetry and legacy/unsigned frames cannot assert absence.
package scheddgrpc_test

import (
	"context"
	"sync"
	"testing"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
)

type inventoryCapturingEngine struct {
	*strictModeEngine
	inventoryMu sync.Mutex
	inventories []sched.NodeInstanceInventory
}

func (e *inventoryCapturingEngine) ObserveNodeInventory(_ context.Context, r sched.NodeInstanceInventory) {
	e.inventoryMu.Lock()
	defer e.inventoryMu.Unlock()
	e.inventories = append(e.inventories, r)
}

func TestReportCapacityOnlyAuthenticatedInventoryAssertsAbsence(t *testing.T) {
	registry, key, keyID := freshStrictModeRegistry(t)
	for _, kind := range []string{"complete-empty", "complete-without-metrics", "legacy", "unsigned", "tampered"} {
		t.Run(kind, func(t *testing.T) {
			mu := sync.Mutex{}
			engine := &inventoryCapturingEngine{strictModeEngine: &strictModeEngine{capturingEngine: &capturingEngine{mu: &mu}, registry: registry}}
			client := newServer(t, engine)
			stream, err := client.ReportCapacity(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			msg := signCapacityReport(t, key, keyID, "node-1", now, 128)
			if kind != "legacy" {
				inventory := sched.NodeInstanceInventory{NodeID: "node-1", NodeKeyID: keyID, SampledAt: time.UnixMilli(msg.SampledAtUnixMs), Complete: true}
				if kind == "complete-without-metrics" {
					inventory.InstanceIDs = []string{"vm-a"}
				}
				signature, err := sched.SignNodeInventory(key, inventory)
				if err != nil {
					t.Fatal(err)
				}
				msg.InstanceInventory = &scheddpb.NodeInstanceInventory{Complete: true, InstanceIds: inventory.InstanceIDs, NodeSignature: signature}
				if kind == "unsigned" {
					msg.InstanceInventory.NodeSignature = nil
				}
				if kind == "tampered" {
					msg.InstanceInventory.InstanceIds = []string{"forged-vm"}
				}
			}
			if err := stream.Send(msg); err != nil {
				t.Fatal(err)
			}
			if _, err := stream.CloseAndRecv(); err != nil {
				t.Fatal(err)
			}
			engine.inventoryMu.Lock()
			defer engine.inventoryMu.Unlock()
			if len(engine.inventories) != 1 {
				t.Fatalf("inventories=%d", len(engine.inventories))
			}
			want := kind == "complete-empty" || kind == "complete-without-metrics"
			if got := engine.inventories[0].Complete; got != want {
				t.Fatalf("complete=%v, want %v", got, want)
			}
		})
	}
}
