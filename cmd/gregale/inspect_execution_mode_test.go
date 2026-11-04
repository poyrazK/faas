package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Production-us rc.294: inspect h14-wk, a running worker with one replica,
// said "app: active · app · http" and "scaling: 0→1 instances ·
// request-driven" (Hunt 14 H14-1).
func TestInspectShowsTheExecutionMode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest api.AppManifest
		want     []string
		unwanted string
	}{
		{name: "worker", manifest: api.AppManifest{ExecutionMode: api.ExecutionModeWorker, WorkerReplicas: &api.WorkerScaling{Min: 1, Max: 3, Metric: "queue_lag"}},
			want: []string{"app:       active · app · worker", "scaling:   1→3 instances · worker pool · scaled by queue_lag"}, unwanted: "request-driven"},
		{name: "service", manifest: api.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &api.ServiceReplicas{Min: 2, Max: 2, Desired: 2}},
			want: []string{"app:       active · app · service", "scaling:   2→2 instances · service replicas"}, unwanted: "request-driven"},
		{name: "request", manifest: api.AppManifest{},
			want: []string{"app:       active · app · http", "request-driven"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := api.AppResponse{ID: "app-1", Slug: "h14-wk", Status: "active", Type: "app", WorkloadClass: "http", Manifest: tc.manifest}
			var buf bytes.Buffer
			main.RenderInspectSummaryHuman(&buf, main.BuildInspectSummary(app, main.InspectSummaryInputs{}))
			out := buf.String()
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("inspect output missing %q:\n%s", want, out)
				}
			}
			if tc.unwanted != "" && strings.Contains(out, tc.unwanted) {
				t.Errorf("inspect output says %q for a %s:\n%s", tc.unwanted, tc.name, out)
			}
		})
	}
}
