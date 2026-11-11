// adr: 20261029174950 — a scheduled delete disables the database's primary
// endpoint, and undo enables it, each confirmed by reading it back.
package neon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type deletionHoldFixture struct {
	mu                 sync.Mutex
	disabled           bool
	patches            []map[string]map[string]any
	ignorePatch        bool
	failConfirmation   bool
	confirmationBranch string
}

func deletionHoldProvider(t *testing.T, f *deletionHoldFixture) *Provider {
	t.Helper()
	const project = "/api/v2/projects/quiet-river-12345678"
	return testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		primary := map[string]any{"id": "ep-main-123", "branch_id": "br-main-123", "type": "read_write", "current_state": "idle", "disabled": f.disabled}
		switch {
		case r.URL.Path == project && r.Method == http.MethodGet:
			writeResponse(t, w, http.StatusOK, map[string]any{"project": map[string]any{"id": "quiet-river-12345678"}})
		case r.URL.Path == project+"/branches":
			writeResponse(t, w, http.StatusOK, map[string]any{"branches": []map[string]any{
				{"id": "br-main-123", "default": true, "current_state": "ready"},
				{"id": "br-restore-123", "default": false, "current_state": "ready"},
			}})
		case r.URL.Path == project+"/endpoints":
			writeResponse(t, w, http.StatusOK, map[string]any{"endpoints": []map[string]any{primary,
				{"id": "ep-restore-123", "branch_id": "br-restore-123", "type": "read_write", "current_state": "idle"}}})
		case r.URL.Path == project+"/endpoints/ep-main-123" && r.Method == http.MethodPatch:
			var payload map[string]map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			f.patches = append(f.patches, payload)
			if !f.ignorePatch {
				f.disabled, _ = payload["endpoint"]["disabled"].(bool)
			}
			writeResponse(t, w, http.StatusOK, map[string]any{"endpoint": primary})
		case r.URL.Path == project+"/endpoints/ep-main-123" && r.Method == http.MethodGet:
			if f.failConfirmation {
				f.failConfirmation = false
				writeResponse(t, w, http.StatusServiceUnavailable, nil)
				return
			}
			primary["disabled"] = f.disabled
			if f.confirmationBranch != "" {
				primary["branch_id"] = f.confirmationBranch
			}
			writeResponse(t, w, http.StatusOK, map[string]any{"endpoint": primary})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			writeResponse(t, w, http.StatusNotFound, nil)
		}
	}))
}

func TestDeletionHoldRecoversAfterConfirmationFailure(t *testing.T) {
	f := &deletionHoldFixture{failConfirmation: true}
	provider := deletionHoldProvider(t, f)
	ctx := context.Background()
	if err := provider.HoldForDeletion(ctx, "quiet-river-12345678/br-main-123"); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("lost confirmation = %v", err)
	}
	f.mu.Lock()
	disabled := f.disabled
	f.mu.Unlock()
	if !disabled {
		t.Fatal("fixture did not reproduce an applied but unconfirmed hold")
	}
	if err := provider.HoldForDeletion(ctx, "quiet-river-12345678/br-main-123"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	patches := len(f.patches)
	f.mu.Unlock()
	if patches != 1 {
		t.Fatalf("retry rewrote the applied hold: %d patches", patches)
	}
	if err := provider.ReleaseDeletionHold(ctx, "quiet-river-12345678/br-main-123"); err != nil {
		t.Fatal(err)
	}
}

func TestDeletionHoldDisablesAndEnablesThePrimaryEndpoint(t *testing.T) {
	f := &deletionHoldFixture{}
	provider := deletionHoldProvider(t, f)
	ctx := context.Background()
	if err := provider.HoldForDeletion(ctx, "quiet-river-12345678/br-main-123"); err != nil || !f.disabled {
		t.Fatalf("hold: %v, disabled=%v", err, f.disabled)
	}
	if err := provider.HoldForDeletion(ctx, "quiet-river-12345678/br-main-123"); err != nil || len(f.patches) != 1 {
		t.Fatalf("a repeated hold wrote again: %v, %d patches", err, len(f.patches))
	}
	if len(f.patches[0]) != 1 || len(f.patches[0]["endpoint"]) != 1 || f.patches[0]["endpoint"]["disabled"] != true {
		t.Fatalf("the hold changed more than the disabled flag: %v", f.patches[0])
	}
	if err := provider.ReleaseDeletionHold(ctx, "quiet-river-12345678/br-main-123"); err != nil || f.disabled || len(f.patches) != 2 {
		t.Fatalf("release: %v, disabled=%v patches=%d", err, f.disabled, len(f.patches))
	}

	f.ignorePatch = true
	if err := provider.HoldForDeletion(ctx, "quiet-river-12345678/br-main-123"); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("an unconfirmed hold: %v", err)
	}
}

func TestDeletionHoldRejectsUnpinnedProject(t *testing.T) {
	f := &deletionHoldFixture{}
	provider := deletionHoldProvider(t, f)
	for _, operation := range []func(context.Context, string) error{provider.HoldForDeletion, provider.ReleaseDeletionHold} {
		if err := operation(t.Context(), "quiet-river-12345678"); !errors.Is(err, managedpostgres.ErrConflict) {
			t.Fatalf("mutable default was accepted: %v", err)
		}
	}
	if len(f.patches) != 0 {
		t.Fatal("unpinned hold changed an endpoint")
	}
}

func TestDeletionHoldRejectsConfirmationFromAnotherBranch(t *testing.T) {
	f := &deletionHoldFixture{confirmationBranch: "br-other-123"}
	provider := deletionHoldProvider(t, f)
	if err := provider.HoldForDeletion(t.Context(), "quiet-river-12345678/br-main-123"); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("another branch confirmed the hold: %v", err)
	}
}

// adr: 20261025094915 — amendment 2026-11-05: the cap sweep reads the same
// hold before it places or releases one.
func TestComputeHeldReadsThePrimaryEndpoint(t *testing.T) {
	f := &deletionHoldFixture{}
	provider := deletionHoldProvider(t, f)
	for _, disabled := range []bool{false, true} {
		f.disabled = disabled
		held, err := provider.ComputeHeld(context.Background(), "quiet-river-12345678")
		if err != nil || held != disabled {
			t.Fatalf("disabled %v: held %v, %v", disabled, held, err)
		}
	}
	if held, err := provider.ComputeHeld(context.Background(), "quiet-river-12345678/br-restore-123"); err != nil || held {
		t.Fatalf("restore branch: held %v, %v; want its own endpoint, which is not disabled", held, err)
	}
	if len(f.patches) != 0 {
		t.Fatalf("reading the hold wrote %v", f.patches)
	}
}
