package conformance

import (
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// testLogArchiveErasure pins the account-deletion snapshot of archived logs:
// it takes the account's live instances and the instances only billing rows
// still name, never another account's, pages them in instance order, keeps
// progress across a repeated begin for the same request, and starts over for
// a new request.
func testLogArchiveErasure(t *testing.T, fx *Fixture) {
	erasure, ok := fx.Store.(state.LogArchiveErasureStore)
	if !ok {
		t.Fatalf("%T does not implement state.LogArchiveErasureStore", fx.Store)
	}
	minute := time.Date(2026, 9, 7, 12, 34, 0, 0, time.UTC)
	perSecond := int64(fx.App.RAMMB + api.PerVMOverheadMB)
	newInstance := func() state.Instance {
		t.Helper()
		ins, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID, string(state.StateRunning), 512, fx.Node.ID, uuid.NewString())
		if err != nil {
			t.Fatalf("CreateInstance: %v", err)
		}
		return ins
	}
	live := newInstance()
	parked := newInstance()
	if err := fx.Store.AppendUsage(fx.Ctx, fx.Account.ID, fx.App.ID, parked.ID, minute, perSecond, 1, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("AppendUsage: %v", err)
	}
	if err := fx.Store.DeleteInstance(fx.Ctx, parked.ID); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}
	other, err := fx.Store.CreateAccount(fx.Ctx, "log-erasure-other-"+uuid.NewString()[:8]+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := fx.Store.AppendUsage(fx.Ctx, other.ID, fx.App.ID, uuid.NewString(), minute, perSecond, 1, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("AppendUsage(other account): %v", err)
	}

	pending := func(limit int) []string {
		t.Helper()
		got, err := erasure.PendingLogArchiveErasure(fx.Ctx, fx.Account.ID, limit)
		if err != nil {
			t.Fatalf("PendingLogArchiveErasure: %v", err)
		}
		return got
	}
	if got := pending(10); len(got) != 0 {
		t.Fatalf("pending before begin = %v, want none", got)
	}
	requested := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	if err := erasure.BeginLogArchiveErasure(fx.Ctx, fx.Account.ID, requested); err != nil {
		t.Fatalf("BeginLogArchiveErasure: %v", err)
	}
	want := []string{live.ID, parked.ID}
	sort.Strings(want)
	if got := pending(10); !slices.Equal(got, want) {
		t.Fatalf("pending = %v, want %v", got, want)
	}
	if got := pending(1); !slices.Equal(got, want[:1]) {
		t.Fatalf("pending(1) = %v, want %v", got, want[:1])
	}

	if err := erasure.MarkLogArchiveErased(fx.Ctx, fx.Account.ID, want[0]); err != nil {
		t.Fatalf("MarkLogArchiveErased: %v", err)
	}
	// An instance that starts after the snapshot is not part of it, and a
	// repeated begin for the same request keeps the progress made.
	_ = newInstance()
	if err := erasure.BeginLogArchiveErasure(fx.Ctx, fx.Account.ID, requested); err != nil {
		t.Fatalf("BeginLogArchiveErasure again: %v", err)
	}
	if got := pending(10); !slices.Equal(got, want[1:]) {
		t.Fatalf("pending after one erased = %v, want %v", got, want[1:])
	}

	// A new deletion request takes a fresh snapshot.
	if err := erasure.BeginLogArchiveErasure(fx.Ctx, fx.Account.ID, requested.Add(time.Hour)); err != nil {
		t.Fatalf("BeginLogArchiveErasure(new request): %v", err)
	}
	if got := pending(10); len(got) != 3 {
		t.Fatalf("pending after a new request = %v, want all 3 instances", got)
	}
	for _, id := range pending(10) {
		if err := erasure.MarkLogArchiveErased(fx.Ctx, fx.Account.ID, id); err != nil {
			t.Fatalf("MarkLogArchiveErased(%s): %v", id, err)
		}
	}
	if got := pending(10); len(got) != 0 {
		t.Fatalf("pending after erasing all = %v, want none", got)
	}
	if got, err := erasure.PendingLogArchiveErasure(fx.Ctx, other.ID, 10); err != nil || len(got) != 0 {
		t.Fatalf("other account pending = %v, %v; want none", got, err)
	}
}
