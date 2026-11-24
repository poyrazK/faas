package neon

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestIdleTailCountsTheWindowsIdleSuspends(t *testing.T) {
	from := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	window := managedpostgres.UsageWindow{From: from, To: from.Add(time.Hour)}
	at := func(minutes int) string {
		return from.Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339Nano)
	}
	op := func(id, action, status, endpointID string, minutes int) map[string]any {
		return map[string]any{"id": id, "action": action, "status": status, "endpoint_id": endpointID, "project_id": "quiet-river-12345678", "created_at": at(minutes)}
	}
	endpoints := []map[string]any{
		{"id": "ep-primary", "project_id": "quiet-river-12345678", "type": "read_write", "autoscaling_limit_min_cu": 0.25, "suspend_timeout_seconds": 120},
		{"id": "ep-restore", "project_id": "quiet-river-12345678", "type": "read_write", "autoscaling_limit_min_cu": 0.5, "suspend_timeout_seconds": 0},
		{"id": "ep-always-on", "project_id": "quiet-river-12345678", "type": "read_write", "autoscaling_limit_min_cu": 1, "suspend_timeout_seconds": -1},
		{"id": "ep-reader", "project_id": "quiet-river-12345678", "type": "read_only", "autoscaling_limit_min_cu": 1, "suspend_timeout_seconds": 60},
	}
	tests := []struct {
		name  string
		pages [][]map[string]any
		want  managedpostgres.IdleTail
		err   error
	}{
		{
			name: "suspends in the window, newest first over two pages",
			pages: [][]map[string]any{
				{op("o9", "suspend_compute", "finished", "ep-primary", 61), op("o8", "suspend_compute", "finished", "ep-primary", 50), op("o7", "start_compute", "finished", "ep-primary", 45)},
				{op("o6", "suspend_compute", "finished", "ep-restore", 30), op("o5", "suspend_compute", "running", "ep-primary", 20),
					op("o4", "suspend_compute", "finished", "ep-always-on", 15), op("o3", "suspend_compute", "finished", "ep-reader", 12),
					op("o2", "suspend_compute", "finished", "ep-deleted", 10), op("o1", "suspend_compute", "finished", "ep-primary", -1)},
			},
			// 120 s × 0.25 CU + the plan's 300 s × 0.5 CU.
			want: managedpostgres.IdleTail{Suspends: 2, ComputeUnitSeconds: 30 + 150},
		},
		{
			name:  "no operations",
			pages: [][]map[string]any{{}},
		},
		{
			name:  "out of order is unavailable",
			pages: [][]map[string]any{{op("o1", "suspend_compute", "finished", "ep-primary", 10), op("o2", "suspend_compute", "finished", "ep-primary", 20)}},
			err:   managedpostgres.ErrUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/projects/quiet-river-12345678/endpoints":
					writeResponse(t, w, http.StatusOK, map[string]any{"endpoints": endpoints})
				case "/api/v2/projects/quiet-river-12345678/operations":
					page := 0
					if cursor := r.URL.Query().Get("cursor"); cursor != "" {
						page = int(cursor[0] - '0')
					}
					next := ""
					if page+1 < len(tt.pages) {
						next = string(rune('0' + page + 1))
					}
					writeResponse(t, w, http.StatusOK, map[string]any{"operations": tt.pages[page], "pagination": map[string]any{"cursor": next}})
				default:
					t.Errorf("unexpected request %s", r.URL)
					writeResponse(t, w, http.StatusNotFound, nil)
				}
			}))
			got, err := provider.IdleTail(context.Background(), "quiet-river-12345678", window)
			if !errors.Is(err, tt.err) || got != tt.want {
				t.Fatalf("IdleTail = %+v, %v; want %+v, %v", got, err, tt.want, tt.err)
			}
		})
	}
}

func TestIdleTailRefusesBranchesAndBadWindows(t *testing.T) {
	provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	}))
	from := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	if _, err := provider.IdleTail(context.Background(), "quiet-river-12345678/br-restore-1234", managedpostgres.UsageWindow{From: from, To: from.Add(time.Hour)}); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Fatalf("branch: %v", err)
	}
	if _, err := provider.IdleTail(context.Background(), "quiet-river-12345678", managedpostgres.UsageWindow{From: from, To: from}); !errors.Is(err, managedpostgres.ErrInvalid) {
		t.Fatalf("empty window: %v", err)
	}
}
