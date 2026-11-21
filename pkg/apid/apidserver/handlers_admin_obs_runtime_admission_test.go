package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type obsRuntimeHeldRegistry struct{ entered chan struct{} }

func (r *obsRuntimeHeldRegistry) ListComputeNodes(ctx context.Context, _ bool) ([]state.ComputeNode, error) {
	r.entered <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestObsRuntimeBuildsAdmissionBusyAndExemptPaths(t *testing.T) {
	e := gapEnv(t)
	gate := (*e.S()).RuntimeBuildAdmissionForTest()
	now := time.Now().UTC()
	query := "?release=" + strings.Repeat("a", 40) + "&after=" + now.Add(-time.Minute).Format(time.RFC3339)
	selected, err := operatorobs.RuntimeBuildsSelection(strings.TrimPrefix(query, "?")+"&expected_role=control-plane-v1", false, now)
	if err != nil {
		t.Fatal(err)
	}
	registry := &obsRuntimeHeldRegistry{entered: make(chan struct{}, api.ObsRuntimeBuildCollectionsMax)}
	var sourceCalls atomic.Int64
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sourceCalls.Add(1)
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer source.Close()
	(*e.S()).WithStatusCache(source.URL, "")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan struct{}, api.ObsRuntimeBuildCollectionsMax)
	for range api.ObsRuntimeBuildCollectionsMax {
		go func() { gate.Collect(ctx, nil, registry, selected, now, nil); finished <- struct{}{} }()
		select {
		case <-registry.entered:
		case <-time.After(time.Second):
			t.Fatal("collection did not enter the shared gate")
		}
	}
	// Rebinding metrics must observe the same gate rather than create capacity.
	ops := wire.NewOpsMetrics("runtime_admission_test")
	(*e.S()).WithOpsMetrics(t.Context(), ops)
	path := operatorobs.RuntimeBuildsPath
	busy := e.Do(t, http.MethodGet, path+query+"&expected_role=single-box-v2", nil, nil)
	var problem api.Problem
	if busy.Code != http.StatusTooManyRequests || json.Unmarshal(busy.Body.Bytes(), &problem) != nil || problem.Code != api.CodeObsRuntimeBusy || problem.Limit == nil || *problem.Limit != api.ObsRuntimeBuildCollectionsMax || problem.Observed == nil || *problem.Observed != api.ObsRuntimeBuildCollectionsMax || problem.DocsURL == "" || busy.Header().Get("Retry-After") != "1" || busy.Header().Get("Cache-Control") != "no-store" || busy.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("busy response: %d %s %v", busy.Code, busy.Body, busy.Header())
	}
	if rec := e.Do(t, http.MethodGet, path+"?release=bad", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("validation should precede admission: %d", rec.Code)
	}
	unauthenticated := httptest.NewRecorder()
	(*e.H()).ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, path+query, nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("authentication should precede admission: %d", unauthenticated.Code)
	}
	if rec := e.Do(t, http.MethodGet, path+query, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("unselected role was refused: %d", rec.Code)
	}
	families, err := ops.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var active, refused float64
	for _, family := range families {
		for _, metric := range family.Metric {
			switch family.GetName() {
			case "apid_operator_runtime_active_collections":
				active = metric.GetGauge().GetValue()
			case "apid_operator_runtime_refused_collections_total":
				refused = metric.GetCounter().GetValue()
			}
		}
	}
	if active != api.ObsRuntimeBuildCollectionsMax || refused != 1 || sourceCalls.Load() != 0 {
		t.Fatalf("active=%v refused=%v source reads=%d", active, refused, sourceCalls.Load())
	}
	(*e.S()).WithOpsMetrics(t.Context(), nil)
	*e.H() = (*e.S()).Handler()
	if rec := e.Do(t, http.MethodGet, path+query+"&expected_role=single-box-v1", nil, nil); rec.Code != http.StatusTooManyRequests || sourceCalls.Load() != 0 {
		t.Fatalf("disabling metrics disabled admission: %d", rec.Code)
	}
	cancel()
	for range api.ObsRuntimeBuildCollectionsMax {
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("cancellation leaked a slot")
		}
	}
	if gate.Active() != 0 {
		t.Fatal("capacity leaked after source cancellation")
	}
}
