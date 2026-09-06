//go:build !no_pg

package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgInstanceReadsPreserveRequestCount(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	appID, _, id := seedFrameworkReadyInstancePg(t, pool)
	store := state.NewPgStore(pool)
	if _, err := store.IncInstanceRequestCount(ctx, id, 51); err != nil {
		t.Fatal(err)
	}
	ready := time.Now().Add(-time.Second)
	if err := store.SetInstanceFrameworkReadyAt(ctx, id, ready); err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T, ins state.Instance, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if ins.RequestCount != 51 || ins.FrameworkReadyAt == nil {
			t.Fatalf("count=%d ready=%v", ins.RequestCount, ins.FrameworkReadyAt)
		}
	}
	ins, err := store.InstanceByID(ctx, id)
	check(t, ins, err)
	migration, err := store.MigrationInstanceByID(ctx, id)
	check(t, migration, err)
	for _, tc := range []struct {
		name string
		read func() ([]state.Instance, error)
	}{
		{"app", func() ([]state.Instance, error) { return store.ListInstancesForApp(ctx, appID) }},
		{"node", func() ([]state.Instance, error) { return store.ListInstancesOnNodeID(ctx, ins.NodeID) }},
		{"live migration", func() ([]state.Instance, error) { return store.ListLiveInstancesOnNode(ctx, ins.NodeID, 100) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := tc.read()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range rows {
				if row.ID == id {
					found = true
					check(t, row, nil)
				}
			}
			if !found {
				t.Fatal("instance absent")
			}
		})
	}
	if err := store.UpdateInstanceStateToTerminal(ctx, id, string(state.StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListInstancesInTerminalStatesOlderThan(ctx, []state.State{state.StateStopped}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("terminal rows=%d", len(rows))
	}
	check(t, rows[0], nil)
}
