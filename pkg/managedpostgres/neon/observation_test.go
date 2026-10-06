// adr: 463 — metadata health never wakes compute or repairs SQL permissions.

package neon

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestObserveRestoredDatabaseIsReadOnlyAndPreservesSuspension(t *testing.T) {
	for _, tc := range []struct {
		state   string
		compute managedpostgres.ComputeState
		status  managedpostgres.ProviderStatus
	}{
		{"idle", managedpostgres.ComputeStateSuspended, managedpostgres.ProviderStatusReady},
		{"active", managedpostgres.ComputeStateActive, managedpostgres.ProviderStatusReady},
		{"starting", managedpostgres.ComputeStateWaking, managedpostgres.ProviderStatusPending},
	} {
		t.Run(tc.state, func(t *testing.T) {
			var requests atomic.Int32
			provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet {
					t.Errorf("health mutation: %s %s", r.Method, r.URL.Path)
				}
				switch r.URL.Path {
				case "/api/v2/projects/quiet-river-12345678":
					writeResponse(t, w, http.StatusOK, map[string]any{"project": map[string]any{"id": "quiet-river-12345678", "region_id": "aws-eu-central-1", "pg_version": 17, "history_retention_seconds": 86400, "settings": map[string]any{"quota": map[string]any{"logical_size_bytes": 10 << 30}}}})
				case "/api/v2/projects/quiet-river-12345678/branches":
					writeResponse(t, w, http.StatusOK, map[string]any{"branches": []map[string]any{{"id": "br-restore-123", "current_state": "ready"}}})
				case "/api/v2/projects/quiet-river-12345678/endpoints":
					writeResponse(t, w, http.StatusOK, map[string]any{"endpoints": []map[string]any{{"id": "ep-restore-123", "branch_id": "br-restore-123", "type": "read_write", "current_state": tc.state, "autoscaling_limit_min_cu": 0.25, "autoscaling_limit_max_cu": 2, "suspend_timeout_seconds": 300}}})
				default:
					t.Errorf("health retrieved credentials or operation history: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			observed, err := provider.Observe(context.Background(), "quiet-river-12345678/br-restore-123")
			if err != nil || observed.ComputeState != tc.compute || observed.Status != tc.status || observed.Spec != testDatabaseSpec() || requests.Load() != 3 {
				t.Fatalf("observation=%+v %v requests=%d", observed, err, requests.Load())
			}
		})
	}
}

func TestObserveDistinguishesMissingResourcesFromAmbiguousMetadata(t *testing.T) {
	ready := branch{ID: "br-main", Default: true, CurrentState: "ready"}
	active := endpoint{ID: "ep-main", BranchID: ready.ID, Type: "read_write", CurrentState: "active"}
	for _, tc := range []struct {
		name      string
		branches  []branch
		endpoints []endpoint
		err       error
		status    managedpostgres.ProviderStatus
	}{
		{"missing branch", []branch{}, nil, managedpostgres.ErrNotFound, ""},
		{"unknown branch inventory", nil, nil, managedpostgres.ErrUnavailable, ""},
		{"ambiguous branch", []branch{ready, ready}, []endpoint{active}, managedpostgres.ErrUnavailable, ""},
		{"missing compute", []branch{ready}, nil, nil, managedpostgres.ProviderStatusFailed},
		{"ambiguous compute", []branch{ready}, []endpoint{active, active}, managedpostgres.ErrUnavailable, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/projects/quiet-river-12345678":
					writeResponse(t, w, http.StatusOK, projectResponse{Project: project{ID: "quiet-river-12345678"}})
				case "/api/v2/projects/quiet-river-12345678/branches":
					writeResponse(t, w, http.StatusOK, branchesResponse{Branches: tc.branches})
				case "/api/v2/projects/quiet-river-12345678/endpoints":
					writeResponse(t, w, http.StatusOK, endpointsResponse{Endpoints: tc.endpoints})
				default:
					t.Errorf("unexpected observation request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			observed, err := provider.Observe(context.Background(), "quiet-river-12345678")
			if !errors.Is(err, tc.err) || observed.Status != tc.status {
				t.Fatalf("observation: %+v %v", observed, err)
			}
		})
	}
}
