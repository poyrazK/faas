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
	mu          sync.Mutex
	disabled    bool
	patches     []map[string]map[string]any
	ignorePatch bool
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
			primary["disabled"] = f.disabled
			writeResponse(t, w, http.StatusOK, map[string]any{"endpoint": primary})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			writeResponse(t, w, http.StatusNotFound, nil)
		}
	}))
}

func TestDeletionHoldDisablesAndEnablesThePrimaryEndpoint(t *testing.T) {
	f := &deletionHoldFixture{}
	provider := deletionHoldProvider(t, f)
	ctx := context.Background()
	if err := provider.HoldForDeletion(ctx, "quiet-river-12345678"); err != nil || !f.disabled {
		t.Fatalf("hold: %v, disabled=%v", err, f.disabled)
	}
	if err := provider.HoldForDeletion(ctx, "quiet-river-12345678"); err != nil || len(f.patches) != 1 {
		t.Fatalf("a repeated hold wrote again: %v, %d patches", err, len(f.patches))
	}
	if len(f.patches[0]) != 1 || len(f.patches[0]["endpoint"]) != 1 || f.patches[0]["endpoint"]["disabled"] != true {
		t.Fatalf("the hold changed more than the disabled flag: %v", f.patches[0])
	}
	if err := provider.ReleaseDeletionHold(ctx, "quiet-river-12345678"); err != nil || f.disabled || len(f.patches) != 2 {
		t.Fatalf("release: %v, disabled=%v patches=%d", err, f.disabled, len(f.patches))
	}

	f.ignorePatch = true
	if err := provider.HoldForDeletion(ctx, "quiet-river-12345678"); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("an unconfirmed hold: %v", err)
	}
}
