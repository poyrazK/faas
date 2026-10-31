// adr: 570, 590
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// invocationSnapshotFailure refuses the invocation version snapshot, optionally
// after running it, and panics on any independent app read.
type invocationSnapshotFailure struct {
	MemStore *state.MemStore
	after    bool
	failure  error
}

func (s invocationSnapshotFailure) AppByID(context.Context, string) (state.App, error) {
	panic("snapshot refusal fell back to an independent read")
}
func (s invocationSnapshotFailure) WithInvocationVersionSnapshot(ctx context.Context, read func(state.InvocationVersionReader) error) error {
	if s.after {
		if err := s.MemStore.WithInvocationVersionSnapshot(ctx, read); err != nil {
			return err
		}
	}
	return s.failure
}

func TestResolveInvocationVersionSnapshotFailurePublishesNoSelection(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "dep-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "dep-" + uuid.NewString(), NodeID: "node-a", RAMMB: 256, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:dep"})
	if err != nil {
		t.Fatal(err)
	}
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 60
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	inv := state.Invocation{AppID: app.ID, AccountID: account.ID, Headers: json.RawMessage(`{"x-gregale-revision":"` + dep.ID + `"}`)}
	failure := errors.New("snapshot unavailable")
	for _, after := range []bool{false, true} {
		failing := statetest.NewMem(invocationSnapshotFailure{MemStore: store, after: after, failure: failure})
		out, version, err := state.ResolveInvocationVersion(ctx, failing, inv)
		if !errors.Is(err, failure) || version != (state.InvocationVersion{}) || !reflect.DeepEqual(out, inv) {
			t.Fatalf("partial selection after=%v: %+v %+v %v", after, out, version, err)
		}
		out, version, owner, err := state.ResolveInvocationDispatch(ctx, failing, inv, nil)
		if !errors.Is(err, failure) || version != (state.InvocationVersion{}) || owner != "" || !reflect.DeepEqual(out, inv) {
			t.Fatalf("partial dispatch after=%v: %+v %+v %q %v", after, out, version, owner, err)
		}
	}
}

func TestInvocationEnvironmentSnapshotFailurePublishesNoSelection(t *testing.T) {
	store := state.NewMemStore()
	f := seedInvocationEnvironment(t, store)
	inv := state.Invocation{AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationAsyncInvoke}
	failure := errors.New("environment snapshot unavailable")
	for _, after := range []bool{false, true} {
		out, version, err := state.ResolveInvocationVersionForEnvironment(t.Context(), statetest.NewMem(invocationSnapshotFailure{store, after, failure}), inv, "staging")
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
