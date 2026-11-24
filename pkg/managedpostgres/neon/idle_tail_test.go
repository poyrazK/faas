package neon

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestIdleTailCountsTheWindowsIdleSuspends(t *testing.T) {
	from := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	window := managedpostgres.UsageWindow{From: from, To: from.Add(time.Hour)}
	at := func(seconds int) string {
		return from.Add(time.Duration(seconds) * time.Second).Format(time.RFC3339Nano)
	}
	op := func(id, action, status, endpointID string, seconds int) map[string]any {
		return map[string]any{"id": id, "action": action, "status": status, "endpoint_id": endpointID, "project_id": "quiet-river-12345678", "created_at": at(seconds)}
	}
	compute := func(id string, minCU float64, timeout int) map[string]any {
		return map[string]any{"id": id, "project_id": "quiet-river-12345678", "type": "read_write", "autoscaling_limit_min_cu": minCU, "suspend_timeout_seconds": timeout}
	}
	reader := map[string]any{"id": "ep-reader", "project_id": "quiet-river-12345678", "type": "read_only", "autoscaling_limit_min_cu": 1, "suspend_timeout_seconds": 60}
	tests := []struct {
		name      string
		endpoints []map[string]any
		pages     [][]map[string]any
		want      managedpostgres.IdleTail
		err       error
		noOps     bool
	}{
		{
			name:      "idle suspends over two pages; restarts, other computes and other windows are not",
			endpoints: []map[string]any{compute("ep-primary", 0.25, 120), reader},
			pages: [][]map[string]any{
				{op("o12", "suspend_compute", "finished", "ep-primary", 3630), op("o11", "start_compute", "finished", "ep-primary", 3010),
					// A restart: suspend, then start within a minute.
					op("o10", "suspend_compute", "finished", "ep-primary", 3000), op("o9", "suspend_compute", "finished", "ep-primary", 2400)},
				{op("o8", "start_compute", "finished", "ep-primary", 1900), op("o7", "suspend_compute", "running", "ep-primary", 1800),
					// A config apply just before the suspend.
					op("o6", "suspend_compute", "finished", "ep-primary", 1230), op("o5", "apply_config", "finished", "ep-primary", 1200),
					op("o4", "suspend_compute", "finished", "ep-reader", 900), op("o3", "suspend_compute", "finished", "ep-primary", 600),
					// Before the window, but within the restart gap: read, not counted.
					op("o2", "suspend_compute", "finished", "ep-primary", -30), op("o1", "suspend_compute", "finished", "ep-primary", -600)},
			},
			// o9 and o3 at 120 s × 0.25 CU.
			want: managedpostgres.IdleTail{Suspends: 2, ComputeUnitSeconds: 60},
		},
		{
			name:      "a timeout of 0 is the plan's 300 s",
			endpoints: []map[string]any{compute("ep-primary", 0.5, 0)},
			pages:     [][]map[string]any{{op("o1", "suspend_compute", "finished", "ep-primary", 600)}},
			want:      managedpostgres.IdleTail{Suspends: 1, ComputeUnitSeconds: 150},
		},
		{
			name:      "a full last page reads one empty page past Neon's cursor",
			endpoints: []map[string]any{compute("ep-primary", 0.25, 120)},
			pages: [][]map[string]any{{op("o4", "suspend_compute", "finished", "ep-primary", 2400), op("o3", "start_compute", "finished", "ep-primary", 1800),
				op("o2", "suspend_compute", "finished", "ep-primary", 1200), op("o1", "start_compute", "finished", "ep-primary", 600)}, nil},
			want: managedpostgres.IdleTail{Suspends: 2, ComputeUnitSeconds: 60},
		},
		{
			name:      "always-on has no tail and no operation read",
			endpoints: []map[string]any{compute("ep-primary", 1, -1)},
			noOps:     true,
		},
		{
			name:      "a restore's second read-write compute is unsupported",
			endpoints: []map[string]any{compute("ep-primary", 0.25, 120), compute("ep-restore", 0.25, 120)},
			noOps:     true,
			err:       managedpostgres.ErrUnsupported,
		},
		{
			name:      "out of order is unavailable",
			endpoints: []map[string]any{compute("ep-primary", 0.25, 120)},
			pages:     [][]map[string]any{{op("o1", "suspend_compute", "finished", "ep-primary", 600), op("o2", "suspend_compute", "finished", "ep-primary", 1200)}},
			err:       managedpostgres.ErrUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/projects/quiet-river-12345678/endpoints":
					writeResponse(t, w, http.StatusOK, map[string]any{"endpoints": tt.endpoints})
				case "/api/v2/projects/quiet-river-12345678/operations":
					if tt.noOps {
						t.Errorf("unexpected operations read")
					}
					page := 0
					if cursor := r.URL.Query().Get("cursor"); cursor != "" {
						page, _ = strconv.Atoi(cursor)
					}
					if page >= len(tt.pages) {
						t.Errorf("read past the last page: %d", page)
						writeResponse(t, w, http.StatusOK, map[string]any{"operations": []any{}})
						return
					}
					// Like Neon, every page carries a cursor.
					writeResponse(t, w, http.StatusOK, map[string]any{"operations": tt.pages[page], "pagination": map[string]any{"cursor": strconv.Itoa(page + 1)}})
				default:
					t.Errorf("unexpected request %s", r.URL)
					writeResponse(t, w, http.StatusNotFound, nil)
				}
			}))
			provider.idleTailPageSize = 4
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
