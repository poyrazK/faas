package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func TestObsRuntimeBuildsMetricsWiring(t *testing.T) {
	e := gapEnv(t)
	ops := *(*e.S()).Ops()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private-runtime-source-error", http.StatusServiceUnavailable)
	}))
	defer source.Close()
	(*e.S()).WithStatusCache(source.URL, "")
	query := "?release=" + strings.Repeat("a", 40) + "&after=" + time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if rec := e.Do(t, http.MethodGet, operatorobs.RuntimeBuildsPath+query+"&expected_role=invalid", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("validation: %d", rec.Code)
	}
	if rec := e.Do(t, http.MethodGet, operatorobs.RuntimeBuildsPath+query, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("unselected: %d", rec.Code)
	}
	rec := e.Do(t, http.MethodGet, operatorobs.RuntimeBuildsPath+query+"&expected_role=single-box-v1", nil, nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "private-runtime-source-error") {
		t.Fatalf("advisory response: %d %s", rec.Code, rec.Body)
	}
	// Reattaching the same registry must retain observations and collectors.
	(*e.S()).WithOpsMetrics(t.Context(), ops)
	families, err := ops.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	reports := map[string]float64{}
	var failedQueries float64
	for _, family := range families {
		for _, metric := range family.Metric {
			labels := map[string]string{}
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			switch family.GetName() {
			case "apid_operator_runtime_reports_total":
				reports[labels["outcome"]] = metric.GetCounter().GetValue()
			case "apid_operator_runtime_query_attempts_total":
				if labels["group"] == "identity" && labels["outcome"] == "source_error" {
					failedQueries += metric.GetCounter().GetValue()
				}
			}
		}
	}
	if reports["unselected"] != 1 || reports["incomplete"] != 1 || failedQueries != 4 {
		t.Fatalf("reports=%v query failures=%v", reports, failedQueries)
	}
}

func TestObsRuntimeBuildsMetricsRejectedRead(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesReadSurface, "ops@faas.dev", "customer@faas.dev")
	rec := e.Do(t, http.MethodGet, operatorobs.RuntimeBuildsPath, nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("scope gate: %d", rec.Code)
	}
	families, err := (*(*e.S()).Ops()).Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "apid_operator_runtime_reports_total" && family.GetName() != "apid_operator_runtime_query_attempts_total" {
			continue
		}
		for _, metric := range family.Metric {
			if metric.GetCounter().GetValue() != 0 {
				t.Fatal("rejected read was recorded as a collection attempt")
			}
		}
	}
}
