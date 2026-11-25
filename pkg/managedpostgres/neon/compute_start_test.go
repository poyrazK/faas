package neon

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestStartComputeStartsOnlyAnIdleUnheldCompute(t *testing.T) {
	primary := func(state string, disabled bool) map[string]any {
		return map[string]any{"id": "ep-main-123", "branch_id": "br-main-123", "type": "read_write", "current_state": state, "disabled": disabled}
	}
	for _, tc := range []struct {
		name      string
		endpoints []map[string]any
		wantStart bool
		wantErr   error
	}{
		{name: "idle", endpoints: []map[string]any{primary("idle", false)}, wantStart: true},
		{name: "running", endpoints: []map[string]any{primary("active", false)}},
		{name: "held", endpoints: []map[string]any{primary("idle", true)}},
		{name: "a read replica is not the compute", endpoints: []map[string]any{primary("idle", false),
			{"id": "ep-read-123", "branch_id": "br-main-123", "type": "read_only", "current_state": "idle"}}, wantStart: true},
		{name: "a restore's second compute", endpoints: []map[string]any{primary("idle", false),
			{"id": "ep-other-123", "branch_id": "br-other-123", "type": "read_write", "current_state": "idle"}}, wantErr: managedpostgres.ErrUnsupported},
		{name: "no compute", endpoints: []map[string]any{}, wantErr: managedpostgres.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var starts atomic.Int32
			provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/quiet-river-12345678/endpoints":
					writeResponse(t, w, http.StatusOK, map[string]any{"endpoints": tc.endpoints})
				case r.Method == http.MethodPost && r.URL.Path == "/api/v2/projects/quiet-river-12345678/endpoints/ep-main-123/start":
					starts.Add(1)
					writeResponse(t, w, http.StatusOK, map[string]any{})
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			started, err := provider.StartCompute(context.Background(), "quiet-river-12345678")
			if !errors.Is(err, tc.wantErr) || tc.wantErr == nil && err != nil {
				t.Fatalf("StartCompute = %v, want %v", err, tc.wantErr)
			}
			if got := starts.Load() == 1; got != tc.wantStart || started != tc.wantStart {
				t.Fatalf("start calls = %v, reported %v, want %v", got, started, tc.wantStart)
			}
		})
	}
}
