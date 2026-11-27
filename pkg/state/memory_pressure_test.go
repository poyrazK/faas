package state_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// The memory pressure read returns the same rows on both stores
// (ADR-20261123190507): RAM pressure evictions, frozen parks under memory
// pressure and OOM kills, newest first, joined to instance, node, app and
// plan; frozen parks for other reasons and unrelated kinds are left out.
func TestMemoryPressureEventsStores(t *testing.T) {
	for _, backend := range []string{"mem", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			var s interface {
				state.MemoryPressureReader
				CreateAccount(context.Context, string, api.Plan) (state.Account, error)
				CreateApp(context.Context, state.App) (state.App, error)
				CreateDeployment(context.Context, state.Deployment) (state.Deployment, error)
				CreateInstance(context.Context, string, string, string, int, string, string) (state.Instance, error)
				ComputeNodeByName(context.Context, string) (state.ComputeNode, error)
				CreateComputeNode(context.Context, state.ComputeNode) (state.ComputeNode, error)
				AppendEventAt(context.Context, string, string, *string, []byte, time.Time) error
			} = state.NewMemStore()
			if backend == "postgres" {
				s = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if errors.Is(err, state.ErrNotFound) {
				node, err = s.CreateComputeNode(ctx, state.ComputeNode{Name: state.DefaultLocalNodeName, TargetURL: "unix:///run/faas/vmmd.sock"})
			}
			if err != nil {
				t.Fatal(err)
			}
			acct, err := s.CreateAccount(ctx, "memory-pressure@example.test", api.PlanHobby)
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "mp-app", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := s.CreateDeployment(ctx, state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage,
				Status: state.DeployLive, ImageDigest: "sha256:" + fmt.Sprintf("%064d", 7)})
			if err != nil {
				t.Fatal(err)
			}
			instance := func() state.Instance {
				t.Helper()
				ins, err := s.CreateInstance(ctx, app.ID, dep.ID, string(state.StateStopped), 512, node.ID, uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
				return ins
			}
			base := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			event := func(kind string, subject *string, data string, at time.Time) {
				t.Helper()
				if err := s.AppendEventAt(ctx, "schedd", kind, subject, []byte(data), at); err != nil {
					t.Fatal(err)
				}
			}
			evicted, parked, killed, ignored := instance(), instance(), instance(), instance()
			event(state.MemoryPressureKindEviction, &evicted.ID, `{"from":"running","to":"stopped","reason":"measured_memory"}`, base.Add(time.Minute))
			event(state.MemoryPressureKindFrozenPark, &parked.ID, `{"from":"frozen","to":"parked","reason":"memory_pressure"}`, base.Add(2*time.Minute))
			event(state.MemoryPressureKindOOMKill, nil, fmt.Sprintf(`{"instance_id":%q,"app_id":%q,"peak_mb":601,"plan_mb":512,"deployment_failed":true}`,
				killed.ID, app.ID), base.Add(3*time.Minute))
			event(state.MemoryPressureKindFrozenPark, &ignored.ID, `{"from":"frozen","to":"parked","reason":"ttl"}`, base.Add(4*time.Minute))
			event("state_transition", &ignored.ID, `{"from":"running","to":"stopped","reason":""}`, base.Add(5*time.Minute))
			// Before the window.
			event(state.MemoryPressureKindEviction, &ignored.ID, `{"reason":"ram_pressure"}`, base.Add(-time.Hour))

			got, err := s.OperatorMemoryPressureEvents(ctx, base, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 3 {
				t.Fatalf("events = %+v", got)
			}
			oom, park, ev := got[0], got[1], got[2]
			if oom.Kind != state.MemoryPressureKindOOMKill || oom.InstanceID != killed.ID || oom.PeakMB != 601 || oom.PlanMB != 512 || !oom.DeploymentFailed ||
				oom.AppSlug != "mp-app" || oom.Plan != "hobby" || oom.NodeName != state.DefaultLocalNodeName {
				t.Fatalf("oom = %+v", oom)
			}
			if park.Kind != state.MemoryPressureKindFrozenPark || park.InstanceID != parked.ID || park.Reason != "memory_pressure" {
				t.Fatalf("frozen park = %+v", park)
			}
			if ev.Kind != state.MemoryPressureKindEviction || ev.InstanceID != evicted.ID || ev.Reason != "measured_memory" || ev.RAMMB != 512 ||
				ev.NodeID != node.ID || ev.AppID != app.ID || ev.AccountID != acct.ID {
				t.Fatalf("eviction = %+v", ev)
			}
			if got, err := s.OperatorMemoryPressureEvents(ctx, base, 1); err != nil || len(got) != 1 || got[0].Kind != state.MemoryPressureKindOOMKill {
				t.Fatalf("limited = %+v err=%v", got, err)
			}
			if _, err := s.OperatorMemoryPressureEvents(ctx, base, 0); err == nil {
				t.Fatal("limit 0: want error")
			}
		})
	}
}
