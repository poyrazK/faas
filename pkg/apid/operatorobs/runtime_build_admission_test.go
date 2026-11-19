package operatorobs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

type runtimeAdmissionRegistry struct {
	calls    atomic.Int64
	entered  chan struct{}
	canceled chan struct{}
	unwind   <-chan struct{}
}

func (r *runtimeAdmissionRegistry) ListComputeNodes(ctx context.Context, _ bool) ([]state.ComputeNode, error) {
	r.calls.Add(1)
	r.entered <- struct{}{}
	<-ctx.Done()
	if r.canceled != nil {
		r.canceled <- struct{}{}
	}
	if r.unwind != nil {
		<-r.unwind
	}
	return nil, ctx.Err()
}

func TestRuntimeBuildAdmissionConcurrentLimitAndRecovery(t *testing.T) {
	for _, instrumented := range []bool{false, true} {
		name := "without_metrics"
		if instrumented {
			name = "with_metrics"
		}
		t.Run(name, func(t *testing.T) {
			var gate RuntimeBuildAdmission
			var metrics *RuntimeDiagnosticsMetrics
			if instrumented {
				metrics = NewRuntimeDiagnosticsMetrics(prometheus.NewPedanticRegistry(), &gate)
			}
			registry := &runtimeAdmissionRegistry{entered: make(chan struct{}, 64)}
			var queries atomic.Int64
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				queries.Add(1)
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			}))
			defer source.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			now := time.Now().UTC().Truncate(time.Second)
			selected := RuntimeBuildSelection{role: "control-plane-v1", release: strings.Repeat("a", 40), after: now.Add(-time.Minute)}
			client := promql.NewClient(source.URL, source.Client())
			const workers = 32
			results := make(chan *api.Problem, workers)
			begin := make(chan struct{})
			for range workers {
				go func() {
					<-begin
					_, problem := gate.Collect(ctx, client, registry, selected, now, metrics)
					results <- problem
				}()
			}
			close(begin)
			for range api.ObsRuntimeBuildCollectionsMax {
				awaitRuntimeAdmission(t, registry.entered)
			}
			for range workers - api.ObsRuntimeBuildCollectionsMax {
				problem := awaitRuntimeAdmission(t, results)
				if problem == nil || problem.Status != http.StatusTooManyRequests || problem.Code != api.CodeObsRuntimeBusy || problem.Limit == nil || *problem.Limit != api.ObsRuntimeBuildCollectionsMax || problem.Observed == nil || *problem.Observed != api.ObsRuntimeBuildCollectionsMax || problem.DocsURL == "" {
					t.Fatalf("unexpected refusal: %+v", problem)
				}
			}
			if gate.Active() != api.ObsRuntimeBuildCollectionsMax || registry.calls.Load() != api.ObsRuntimeBuildCollectionsMax || queries.Load() != 0 {
				t.Fatal("refused collections reached sources or concurrency exceeded the cap")
			}
			if metrics != nil {
				assertRuntimeAdmissionGauge(t, metrics, api.ObsRuntimeBuildCollectionsMax)
				if testutil.ToFloat64(metrics.refused) != workers-api.ObsRuntimeBuildCollectionsMax {
					t.Fatal("refusal count did not match rejected work")
				}
			}

			// Cheap unselected and already-canceled requests need no slot or I/O.
			unselected := selected
			unselected.role = ""
			if out, problem := gate.Collect(t.Context(), client, registry, unselected, now, metrics); problem != nil || out.Reason != "expected_role_not_selected" {
				t.Fatal("unselected read consumed admission capacity")
			}
			canceled, stop := context.WithCancel(t.Context())
			stop()
			if _, problem := gate.Collect(canceled, client, registry, selected, now, metrics); problem != nil {
				t.Fatal("preflight cancellation became a busy refusal")
			}
			if gate.Active() != api.ObsRuntimeBuildCollectionsMax || registry.calls.Load() != api.ObsRuntimeBuildCollectionsMax || queries.Load() != 0 {
				t.Fatal("cheap read used a slot or reached a source")
			}

			cancel()
			for range api.ObsRuntimeBuildCollectionsMax {
				if problem := awaitRuntimeAdmission(t, results); problem != nil {
					t.Fatalf("an admitted cancellation became a refusal: %+v", problem)
				}
			}
			if gate.Active() != 0 {
				t.Fatal("cancellation leaked capacity")
			}
			if metrics != nil {
				assertRuntimeAdmissionGauge(t, metrics, 0)
			}

			// A fresh read can use capacity immediately after cancellation unwinds.
			fresh, stopFresh := context.WithCancel(t.Context())
			defer stopFresh()
			go func() {
				_, problem := gate.Collect(fresh, client, registry, selected, now, metrics)
				results <- problem
			}()
			awaitRuntimeAdmission(t, registry.entered)
			if gate.Active() != 1 {
				t.Fatal("capacity was not reusable")
			}
			stopFresh()
			if problem := awaitRuntimeAdmission(t, results); problem != nil || gate.Active() != 0 {
				t.Fatal("fresh read leaked or failed admission")
			}
		})
	}
}

func TestRuntimeBuildAdmissionKeepsCanceledWorkUntilItUnwinds(t *testing.T) {
	var gate RuntimeBuildAdmission
	unwind := make(chan struct{})
	release := sync.OnceFunc(func() { close(unwind) })
	defer release()
	registry := &runtimeAdmissionRegistry{entered: make(chan struct{}, 3), canceled: make(chan struct{}, 3), unwind: unwind}
	metrics := NewRuntimeDiagnosticsMetrics(prometheus.NewPedanticRegistry(), &gate)
	now := time.Now().UTC()
	selected := RuntimeBuildSelection{role: "control-plane-v1", release: strings.Repeat("a", 40), after: now.Add(-time.Minute)}
	contexts := make([]context.Context, api.ObsRuntimeBuildCollectionsMax+1)
	cancels := make([]context.CancelFunc, len(contexts))
	results := make([]chan *api.Problem, len(contexts))
	for i := range contexts {
		contexts[i], cancels[i] = context.WithCancel(t.Context())
		defer cancels[i]()
		results[i] = make(chan *api.Problem, 1)
	}
	start := func(i int) {
		go func() {
			_, problem := gate.Collect(contexts[i], nil, registry, selected, now, metrics)
			results[i] <- problem
		}()
		awaitRuntimeAdmission(t, registry.entered)
	}
	for i := range api.ObsRuntimeBuildCollectionsMax {
		start(i)
	}
	cancels[0]()
	awaitRuntimeAdmission(t, registry.canceled)
	if gate.Active() != api.ObsRuntimeBuildCollectionsMax {
		t.Fatal("cancellation released capacity while source work was still running")
	}
	busy := make(chan *api.Problem, 1)
	go func() {
		_, problem := gate.Collect(t.Context(), nil, registry, selected, now, metrics)
		busy <- problem
	}()
	if problem := awaitRuntimeAdmission(t, busy); problem == nil {
		t.Fatal("unwinding work did not retain its slot")
	}
	release()
	if problem := awaitRuntimeAdmission(t, results[0]); problem != nil || gate.Active() != api.ObsRuntimeBuildCollectionsMax-1 {
		t.Fatal("finishing one collection released another collection's slot")
	}
	start(api.ObsRuntimeBuildCollectionsMax)
	if gate.Active() != api.ObsRuntimeBuildCollectionsMax {
		t.Fatal("released slot could not be reused")
	}
	for i := 1; i < len(contexts); i++ {
		cancels[i]()
	}
	for i := 1; i < len(contexts); i++ {
		if problem := awaitRuntimeAdmission(t, results[i]); problem != nil {
			t.Fatal("accepted work became a busy response")
		}
	}
	assertRuntimeAdmissionGauge(t, metrics, 0)
}

func TestRuntimeBuildAdmissionReturnsCapacityAfterSuccessAndFailure(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		var gate RuntimeBuildAdmission
		reg := prometheus.NewPedanticRegistry()
		metrics := NewRuntimeDiagnosticsMetrics(reg, &gate)
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
		}))
		now := time.Now().UTC()
		selected := RuntimeBuildSelection{role: "single-box-v1", release: strings.Repeat("a", 40), after: now.Add(-time.Minute)}
		for range api.ObsRuntimeBuildCollectionsMax + 1 {
			if _, problem := gate.Collect(t.Context(), promql.NewClient(source.URL, source.Client()), nil, selected, now, metrics); problem != nil || gate.Active() != 0 {
				t.Fatal("completed collection leaked capacity")
			}
		}
		source.Close()
		assertRuntimeAdmissionGauge(t, metrics, 0)
		if testutil.ToFloat64(metrics.refused) != 0 {
			t.Fatal("sequential completed reads were refused")
		}
	}
}

func TestRuntimeDiagnosticsMetricsReuseRegistry(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	var first, second RuntimeBuildAdmission
	metrics := NewRuntimeDiagnosticsMetrics(reg, &first)
	if _, admitted := first.tryAcquire(); !admitted {
		t.Fatal("first gate refused an available slot")
	}
	defer first.active.Add(-1)
	metrics.refused.Inc()
	assertRuntimeAdmissionGauge(t, metrics, 1)

	reused := NewRuntimeDiagnosticsMetrics(reg, &second)
	if reused != metrics || testutil.ToFloat64(reused.refused) != 1 {
		t.Fatal("reusing the registry lost the existing observations")
	}
	assertRuntimeAdmissionGauge(t, reused, 0)
	reused.BindAdmission(&first)
	assertRuntimeAdmissionGauge(t, reused, 1)
	assertRuntimeMetricsPrivate(t, reg)
}

func awaitRuntimeAdmission[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime admission")
	}
	var zero T
	return zero
}

func assertRuntimeAdmissionGauge(t *testing.T, metrics *RuntimeDiagnosticsMetrics, expected float64) {
	t.Helper()
	var metric dto.Metric
	if err := metrics.active.Write(&metric); err != nil {
		t.Fatal(err)
	}
	if metric.GetGauge().GetValue() != expected {
		t.Fatalf("active metric=%v want %v", metric.GetGauge().GetValue(), expected)
	}
}
