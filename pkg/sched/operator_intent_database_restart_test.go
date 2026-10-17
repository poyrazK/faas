// adr: 1027 — database_restart operator intents restart a native database
// in place through the database scheduler and are stamped by its answer.
package sched

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type fakeDatabaseRestarter struct {
	ids []string
	err error
}

func (f *fakeDatabaseRestarter) RestartDatabase(_ context.Context, id string) (string, error) {
	f.ids = append(f.ids, id)
	return "running", f.err
}

func TestDatabaseRestartIntentRestartsTheDatabase(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	engine, err := NewEngine(ctx, store, NewNodeLedger(), nil, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	restarter := &fakeDatabaseRestarter{}
	loop := (&Loop{engine: engine, log: silenceLog()}).WithDatabaseRestarter(restarter)
	const databaseID = "0f6f3a52-6c0e-4c55-8e0a-2a0b9d6c1d11"
	run := func() state.OperatorIntent {
		t.Helper()
		id, err := store.InsertOperatorIntent(ctx, state.OperatorIntentKindDatabaseRestart, databaseID, nil, "operator-1", "minor_update", nil, nil)
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
	if got := run(); got.Status != state.OperatorIntentSucceeded || len(restarter.ids) != 1 || restarter.ids[0] != databaseID {
		t.Fatalf("intent %+v, restarts %v", got, restarter.ids)
	}
	restarter.err = errors.New("dbsched: conflict: the database is moving")
	if got := run(); got.Status != state.OperatorIntentFailed || got.Error != "database_restart: dbsched: conflict: the database is moving" {
		t.Fatalf("refused intent %+v", got)
	}
	loop.databaseRestarter = nil
	if got := run(); got.Status != state.OperatorIntentFailed {
		t.Fatalf("intent without a database scheduler %+v", got)
	}
}
