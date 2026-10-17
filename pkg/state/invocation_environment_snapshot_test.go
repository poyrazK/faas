// adr: 570, 590
package state_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type environmentSnapshotFailure struct {
	*state.MemStore
	after   bool
	failure error
}

func (s environmentSnapshotFailure) AppByID(context.Context, string) (state.App, error) {
	panic("environment snapshot refusal fell back to an independent read")
}
func (s environmentSnapshotFailure) WithInvocationVersionSnapshot(ctx context.Context, read func(state.InvocationVersionReader) error) error {
	if s.after {
		if err := s.MemStore.WithInvocationVersionSnapshot(ctx, read); err != nil {
			return err
		}
	}
	return s.failure
}

func TestInvocationEnvironmentSnapshotFailurePublishesNoSelection(t *testing.T) {
	store := state.NewMemStore()
	f := seedInvocationEnvironment(t, store)
	inv := state.Invocation{AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationAsyncInvoke}
	failure := errors.New("environment snapshot unavailable")
	for _, after := range []bool{false, true} {
		out, version, err := state.ResolveInvocationVersionForEnvironment(t.Context(), environmentSnapshotFailure{store, after, failure}, inv, "staging")
		if !errors.Is(err, failure) || version != (state.InvocationVersion{}) || !reflect.DeepEqual(out, inv) {
			t.Fatalf("partial environment selection after=%v: %+v %+v %v", after, out, version, err)
		}
	}
}

func TestMemInvocationEnvironmentSnapshotDispatch(t *testing.T) {
	testInvocationEnvironmentSnapshotDispatch(t, state.NewMemStore())
}
func testInvocationEnvironmentSnapshotDispatch(t *testing.T, store invocationEnvironmentTestStore) {
	t.Helper()
	f := seedInvocationEnvironment(t, store)
	request := state.Invocation{AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationAsyncInvoke}
	prepared, version, err := state.ResolveInvocationVersionForEnvironment(t.Context(), store, request, "staging")
	if err != nil {
		t.Fatal(err)
	}
	queued, err := store.EnqueueInvocation(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	actual, selected, owner, err := state.ResolveInvocationDispatch(t.Context(), store, queued, nil)
	if err != nil || selected != version || owner != f.account.ID || actual.EnvironmentID == "" || actual.EnvironmentID != queued.EnvironmentID {
		t.Fatalf("owned stage dispatch = %+v %+v %q %v", actual, selected, owner, err)
	}
	forged := queued
	forged.EnvironmentID = uuid.NewString()
	out, selected, owner, err := state.ResolveInvocationDispatch(t.Context(), store, forged, nil)
	if !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) || selected != (state.InvocationVersion{}) || owner != "" || !reflect.DeepEqual(out, forged) {
		t.Fatalf("forged environment published selection: %+v %+v %q %v", out, selected, owner, err)
	}
}
