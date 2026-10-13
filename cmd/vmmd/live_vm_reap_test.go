package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type fakeLiveVMs struct {
	ids        []string
	destroyed  []string
	destroyErr error
}

func (f *fakeLiveVMs) AppInstanceIDs() []string { return slices.Clone(f.ids) }

func (f *fakeLiveVMs) Destroy(_ context.Context, id string) error {
	if f.destroyErr != nil {
		return f.destroyErr
	}
	f.destroyed = append(f.destroyed, id)
	f.ids = slices.DeleteFunc(f.ids, func(s string) bool { return s == id })
	return nil
}

type fakeRows map[string]string

func (f fakeRows) InstanceByID(_ context.Context, id string) (state.Instance, error) {
	st, ok := f[id]
	if !ok {
		return state.Instance{}, state.ErrNotFound
	}
	return state.Instance{ID: id, State: st}, nil
}

// TestLiveVMReapDestroysVMsWhoseRowIsNotLive pins production-us hunt #9,
// H9-171: 53 VMs kept running for hours behind parked rows because nothing
// reconciled that direction while vmmd was up.
func TestLiveVMReapDestroysVMsWhoseRowIsNotLive(t *testing.T) {
	ctx := context.Background()
	vms := &fakeLiveVMs{ids: []string{"parked", "running", "no-row", "revived", "stopped"}}
	rows := fakeRows{"parked": "parked", "running": "running", "revived": "parked", "stopped": "stopped"}
	r := &liveVMReaper{vms: vms, rows: rows, log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	if n := r.sweep(ctx); n != 0 || len(vms.destroyed) != 0 {
		t.Fatalf("first sweep destroyed %v; a single sighting must not destroy", vms.destroyed)
	}
	rows["revived"] = "running"
	if n := r.sweep(ctx); n != 2 || !slices.Equal(vms.destroyed, []string{"parked", "stopped"}) {
		t.Fatalf("second sweep destroyed %v (%d); want [parked stopped]", vms.destroyed, n)
	}
	if n := r.sweep(ctx); n != 0 {
		t.Fatalf("third sweep destroyed %d more; want 0", n)
	}
}

func TestLiveVMReapRetriesAFailedDestroy(t *testing.T) {
	ctx := context.Background()
	vms := &fakeLiveVMs{ids: []string{"parked"}, destroyErr: errors.New("teardown busy")}
	r := &liveVMReaper{vms: vms, rows: fakeRows{"parked": "parked"}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r.sweep(ctx)
	r.sweep(ctx)
	vms.destroyErr = nil
	if n := r.sweep(ctx); n != 1 {
		t.Fatalf("retry destroyed %d; want 1", n)
	}
}
