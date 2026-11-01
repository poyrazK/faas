// adr: 1027 — database_move operator intents start a native database's
// move through the database scheduler and are stamped by its answer.
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type fakeDatabaseMover struct {
	id, target string
	err        error
}

func (f *fakeDatabaseMover) StartDatabaseMove(_ context.Context, id, target string) (DatabaseMove, error) {
	f.id, f.target = id, target
	if f.err != nil {
		return DatabaseMove{}, f.err
	}
	return DatabaseMove{From: "db-1", To: "db-2"}, nil
}

func TestDatabaseMoveIntentStartsTheMove(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	engine, err := NewEngine(ctx, store, NewNodeLedger(), nil, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	mover := &fakeDatabaseMover{}
	loop := (&Loop{engine: engine, log: silenceLog()}).WithDatabaseMover(mover)
	const databaseID = "0f6f3a52-6c0e-4c55-8e0a-2a0b9d6c1d11"

	insert := func(metadata string) string {
		t.Helper()
		id, err := store.InsertOperatorIntent(ctx, state.OperatorIntentKindDatabaseMove, databaseID, nil, "operator-1", "rebalance", json.RawMessage(metadata), nil)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	status := func(id string) state.OperatorIntent {
		t.Helper()
		got, err := store.GetOperatorIntent(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	ok := insert(`{"target_node":"db-2"}`)
	loop.drainPendingOperatorIntents(ctx)
	if got := status(ok); got.Status != state.OperatorIntentSucceeded || mover.id != databaseID || mover.target != "db-2" {
		t.Fatalf("intent %+v, mover %+v", got, mover)
	}

	mover.err = errors.New("dbsched: no database node has capacity")
	refused := insert(`{}`)
	loop.drainPendingOperatorIntents(ctx)
	if got := status(refused); got.Status != state.OperatorIntentFailed || got.Error != "database_move: dbsched: no database node has capacity" || mover.target != "" {
		t.Fatalf("refused intent %+v", got)
	}

	// A schedd without the database scheduler fails the intent.
	loop.databaseMover = nil
	disabled := insert(`{}`)
	loop.drainPendingOperatorIntents(ctx)
	if got := status(disabled); got.Status != state.OperatorIntentFailed {
		t.Fatalf("intent without a database scheduler %+v", got)
	}
}
