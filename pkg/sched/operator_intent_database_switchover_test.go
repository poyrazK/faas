// adr: 1027 — database_switchover operator intents hand a production-class
// native database to its standby through the database scheduler and are
// stamped by its answer.
package sched

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type fakeDatabaseSwitcher struct {
	ids []string
	err error
}

func (f *fakeDatabaseSwitcher) SwitchoverDatabase(_ context.Context, id string) (string, error) {
	f.ids = append(f.ids, id)
	return "db-b", f.err
}

func TestDatabaseSwitchoverIntentSwitchesTheDatabaseOver(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	engine, err := NewEngine(ctx, store, NewNodeLedger(), nil, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	switcher := &fakeDatabaseSwitcher{}
	loop := (&Loop{engine: engine, log: silenceLog()}).WithDatabaseSwitcher(switcher)
	const databaseID = "0f6f3a52-6c0e-4c55-8e0a-2a0b9d6c1d11"
	run := func() state.OperatorIntent {
		t.Helper()
		id, err := store.InsertOperatorIntent(ctx, state.OperatorIntentKindDatabaseSwitchover, databaseID, nil, "operator-1", "node_maintenance", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		loop.drainPendingOperatorIntents(ctx)
		got, err := store.GetOperatorIntent(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := run(); got.Status != state.OperatorIntentSucceeded || len(switcher.ids) != 1 || switcher.ids[0] != databaseID {
		t.Fatalf("intent %+v, switchovers %v", got, switcher.ids)
	}
	switcher.err = errors.New("dbsched: conflict: no synchronized standby to switch over to")
	if got := run(); got.Status != state.OperatorIntentFailed || got.Error != "database_switchover: dbsched: conflict: no synchronized standby to switch over to" {
		t.Fatalf("refused intent %+v", got)
	}
	loop.databaseSwitcher = nil
	if got := run(); got.Status != state.OperatorIntentFailed {
		t.Fatalf("intent without a database scheduler %+v", got)
	}
}
