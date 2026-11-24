// adr: 20261123062111 — D2: the history limit shortens a project's restore
// window and gives the spec's back.
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

type historyRetentionFixture struct {
	mu          sync.Mutex
	retention   *int64
	patches     []map[string]any
	ignorePatch bool
}

func historyRetentionProvider(t *testing.T, f *historyRetentionFixture) *Provider {
	t.Helper()
	const project = "/api/v2/projects/quiet-river-12345678"
	return testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == project && r.Method == http.MethodGet:
			body := map[string]any{"id": "quiet-river-12345678"}
			if f.retention != nil {
				body["history_retention_seconds"] = *f.retention
			}
			writeResponse(t, w, http.StatusOK, map[string]any{"project": body})
		case r.URL.Path == project && r.Method == http.MethodPatch:
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			f.patches = append(f.patches, payload)
			if value, ok := payload["project"].(map[string]any)["history_retention_seconds"].(float64); ok && !f.ignorePatch {
				v := int64(value)
				f.retention = &v
			}
			writeResponse(t, w, http.StatusOK, map[string]any{"project": map[string]any{"id": "quiet-river-12345678"}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			writeResponse(t, w, http.StatusNotFound, nil)
		}
	}))
}

func TestHistoryRetention(t *testing.T) {
	week := int64(604800)
	f := &historyRetentionFixture{retention: &week}
	provider := historyRetentionProvider(t, f)
	// A restore branch shares its source project's window.
	observed, err := provider.ObserveHistoryRetention(context.Background(), "quiet-river-12345678/br-restore-123")
	if err != nil || observed.Scope != "quiet-river-12345678" || observed.Seconds != week {
		t.Fatalf("observed %+v, %v", observed, err)
	}
	if err := provider.SetHistoryRetention(context.Background(), "quiet-river-12345678", 86400); err != nil {
		t.Fatal(err)
	}
	if len(f.patches) != 1 || len(f.patches[0]["project"].(map[string]any)) != 1 || *f.retention != 86400 {
		t.Fatalf("patch %v changed more than the restore window, or did not change it (%d)", f.patches, *f.retention)
	}
	for _, seconds := range []int64{0, -1, week + 1} {
		if err := provider.SetHistoryRetention(context.Background(), "quiet-river-12345678", seconds); !errors.Is(err, managedpostgres.ErrInvalid) {
			t.Fatalf("window %d: %v; zero turns point-in-time restore off and the backend offers at most a week", seconds, err)
		}
	}
	if len(f.patches) != 1 {
		t.Fatalf("a refused window was sent: %v", f.patches)
	}
	f.ignorePatch = true
	if err := provider.SetHistoryRetention(context.Background(), "quiet-river-12345678", week); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("an unconfirmed write: %v", err)
	}
	f.retention = nil
	if _, err := provider.ObserveHistoryRetention(context.Background(), "quiet-river-12345678"); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("a project without a window: %v", err)
	}
}
