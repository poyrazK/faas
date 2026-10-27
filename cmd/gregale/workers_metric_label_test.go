package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Production-us rc.294: `workers status h14-wk` showed "Metric: queue_lag"
// although its scaling policy had no target, and `workers scale` printed
// "Scaling: target=0, metric=" for the same app (Hunt 14 H14-58).
func TestWorkerScalingMetricsFollowThePolicy(t *testing.T) {
	for _, tc := range []struct {
		name string
		app  api.AppResponse
		want string
	}{
		{"no policy target or manifest metric", api.AppResponse{ScalingPolicy: &api.ScalingPolicy{}}, "none (holds the minimum replicas)"},
		{"policy target", api.AppResponse{ScalingPolicy: &api.ScalingPolicy{Target: &api.ScalingTarget{Metric: "queue_depth"}}}, "queue_depth"},
		{"multi-signal policy", api.AppResponse{ScalingPolicy: &api.ScalingPolicy{Targets: []api.ScalingTarget{{Metric: "queue_lag"}, {Metric: "custom"}}}}, "queue_lag, custom"},
		{"manifest metric only", api.AppResponse{Manifest: api.AppManifest{WorkerReplicas: &api.WorkerScaling{Metric: "queue_lag"}}}, "queue_lag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := main.WorkerMetricLabel(main.WorkerScalingMetrics(tc.app)); got != tc.want {
				t.Fatalf("metric = %q, want %q", got, tc.want)
			}
		})
	}
}
