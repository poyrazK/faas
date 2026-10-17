// adr: 570
// adr: 521
package acceptance_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type operationVersionAfterApp struct {
	state.InvocationVersionReader
	after func()
}

func (s operationVersionAfterApp) AppByID(ctx context.Context, id string) (state.App, error) {
	app, err := s.InvocationVersionReader.AppByID(ctx, id)
	if err == nil {
		s.after()
	}
	return app, err
}

type operationSnapshotMutationStore struct {
	*state.PgStore
	after func()
}

func (s operationSnapshotMutationStore) WithInvocationVersionSnapshot(ctx context.Context, read func(state.InvocationVersionReader) error) error {
	return s.PgStore.WithInvocationVersionSnapshot(ctx, func(reader state.InvocationVersionReader) error {
		return read(operationVersionAfterApp{InvocationVersionReader: reader, after: s.after})
	})
}

func TestPgOperationInvocationUsesOneCommittedSnapshot(t *testing.T) {
	store, _ := pgStore(t)
	ctx, account, _, definition, tenant, _ := operationFixture(t, store)
	op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: account.ID,
		PlatformTenantID: tenant.ID, DefinitionID: definition.ID,
		IdempotencyKey: "snapshot-cancel", Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := store.InvocationByID(ctx, op.CurrentInvocationID)
	if err != nil {
		t.Fatal(err)
	}
	mutating := operationSnapshotMutationStore{PgStore: store, after: func() {
		if _, err := store.CancelOperation(ctx, account.ID, tenant.ID, op.ID, op.Generation); err != nil {
			t.Fatal(err)
		}
	}}
	prepared, version, owner, err := state.ResolveInvocationDispatch(ctx, mutating, inv, nil)
	if err != nil || owner != account.ID || version.DeploymentID != definition.DeploymentID || version.Scope != definition.Scope || !reflect.DeepEqual(prepared, inv) {
		t.Fatalf("mixed operation snapshot: %+v %+v %q %v", prepared, version, owner, err)
	}
	prepared, version, owner, err = state.ResolveInvocationDispatch(ctx, store, inv, nil)
	if !errors.Is(err, state.ErrOperationStaleAttempt) || owner != "" || version != (state.InvocationVersion{}) || !reflect.DeepEqual(prepared, inv) {
		t.Fatalf("fresh canceled operation escaped refusal: %+v %+v %q %v", prepared, version, owner, err)
	}
}
