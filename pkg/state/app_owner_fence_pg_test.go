//go:build !no_pg

// adr: 20261028200212
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppOwnerFencePostgres(t *testing.T) {
	store, _ := lifecycleCompletionPGStore(t)
	if !state.AdmissionFunctionsUsable(t.Context(), store) {
		t.Fatal("migrated database lacks the admission functions")
	}
	t.Run("function", func(t *testing.T) { checkAppOwnerFence(t, store) })
	t.Run("statements", func(t *testing.T) { checkAppOwnerFence(t, state.StatementAdmissionStore(store)) })
}

// Both interleavings of an admission and a parked-only move, held open with
// a raw transaction so each order is deterministic.
func TestAppOwnerFencePostgresInterleavings(t *testing.T) {
	store, pool := lifecycleCompletionPGStore(t)
	for _, tc := range []struct {
		name  string
		store state.Store
	}{
		{"function", store},
		{"statements", state.StatementAdmissionStore(store)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			app, dep := completionApplication(t, tc.store)
			a := completionNode(t, tc.store, state.NodeLifecycleActive)
			b := completionNode(t, tc.store, state.NodeLifecycleActive)
			if err := tc.store.SetAppNodeID(ctx, app, a.ID); err != nil {
				t.Fatal(err)
			}

			// A move in progress holds the app row: the owner's admission
			// waits for it, then sees the new owner and refuses.
			move, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = move.Rollback(ctx) }()
			if _, err := move.Exec(ctx, `select 1 from apps where id = $1 for update`, app); err != nil {
				t.Fatal(err)
			}
			admitted := make(chan error, 1)
			go func() {
				_, err := tc.store.CreateInstanceWithMode(state.WithAppOwnerFence(ctx, a.ID), app, dep, string(state.StateWaking), 128, a.ID, uuid.NewString(), string(state.InstanceModeNormal))
				admitted <- err
			}()
			select {
			case err := <-admitted:
				t.Fatalf("admission did not wait for the move: %v", err)
			case <-time.After(300 * time.Millisecond):
			}
			if _, err := move.Exec(ctx, `update apps set node_id = $2, reassigned_at = now() where id = $1`, app, b.ID); err != nil {
				t.Fatal(err)
			}
			if err := move.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-admitted; !errors.Is(err, state.ErrAppOwnerMoved) {
				t.Fatalf("admission after the move = %v, want ErrAppOwnerMoved", err)
			}

			// An admission in progress holds the app row (its insert's
			// foreign key): the move waits for it, then sees the instance.
			admission, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = admission.Rollback(ctx) }()
			if _, err := admission.Exec(ctx,
				`insert into instances (app_id, deployment_id, state, ram_mb, node_id, wake_id, started_at, mode)
				 values ($1, $2, 'waking', 128, $3, gen_random_uuid(), now(), 'normal')`, app, dep, b.ID); err != nil {
				t.Fatal(err)
			}
			moved := make(chan error, 1)
			go func() { moved <- store.ReassignParkedAppOwner(ctx, app, b.ID, a.ID) }()
			select {
			case err := <-moved:
				t.Fatalf("move did not wait for the admission: %v", err)
			case <-time.After(300 * time.Millisecond):
			}
			if err := admission.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-moved; !errors.Is(err, state.ErrAppResident) {
				t.Fatalf("move after the admission = %v, want ErrAppResident", err)
			}
		})
	}
}
