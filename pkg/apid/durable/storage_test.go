// adr: 712
package durable

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

func TestDurableEntityStorageLimitProblemsAndPendingAccounting(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		err        error
		status     int
	}{
		{"cap", "durable_entity_storage_limit", &durableentity.LimitError{Budget: "committed_storage_bytes", Limit: 1000, Observed: 1500}, http.StatusConflict},
		{"migration", "durable_entity_inventory_pending", durableentity.ErrInventoryPending, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			WriteProblem(rec, errors.Join(tc.err, errors.New("private-provider-detail")))
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || rec.Code != tc.status || problem.Code != tc.code {
				t.Fatal(rec.Code, rec.Body.String(), err)
			}
			if tc.name == "cap" && (problem.Limit == nil || *problem.Limit != 1000 || problem.Observed == nil || *problem.Observed != 1500 || problem.DocsURL == "") {
				t.Fatal("missing RFC 7807 limit detail", problem)
			}
		})
	}
}

func TestDurableEntityStorageLimitConfigurationIsExplicit(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		want  int64
		valid bool
	}{{"", 0, true}, {"1", 1, true}, {"9223372036854775807", 9223372036854775807, true},
		{"0", 0, false}, {"-1", 0, false}, {"+1", 0, false}, {"01", 0, false}, {" 1", 0, false},
		{"9223372036854775808", 0, false}, {"unknown", 0, false}} {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := durableEntityStorageLimit(tc.raw)
			if (err == nil) != tc.valid || tc.valid && got != tc.want {
				t.Fatal(got, err)
			}
		})
	}
}

func TestDurableEntityInventoryMetricsUseCompletedSamples(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := NewMetrics(registry, "apid")
	m.ObserveMaintenance(durableentity.MaintenanceResult{Inventory: &durableentity.InventoryResult{Usage: durableentity.StorageUsage{ReceiptBytes: 100}}}, nil)
	m.ObserveMaintenance(durableentity.MaintenanceResult{Inventory: &durableentity.InventoryResult{Complete: true, CurrentBytesKnown: true, CurrentBytes: 500, Usage: durableentity.StorageUsage{ReceiptBytes: 200}}}, nil)
	m.ObserveMaintenance(durableentity.MaintenanceResult{Inventory: &durableentity.InventoryResult{Complete: true, Usage: durableentity.StorageUsage{ReceiptBytes: 300}}}, nil)
	if testutil.ToFloat64(m.inventory.WithLabelValues("partial")) != 1 || testutil.ToFloat64(m.inventory.WithLabelValues("complete")) != 1 || testutil.ToFloat64(m.inventory.WithLabelValues("current_bytes_unavailable")) != 1 {
		t.Fatal("inventory quality not recorded")
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "apid_durable_entity_committed_bytes" {
			if sample := family.Metric[0].Histogram; sample.GetSampleCount() != 2 || sample.GetSampleSum() != 500 {
				t.Fatal("partial or unknown sample treated as retained inventory", sample)
			}
		}
		if family.GetName() == "apid_durable_entity_current_key_bytes" {
			if sample := family.Metric[0].Histogram; sample.GetSampleCount() != 1 || sample.GetSampleSum() != 500 {
				t.Fatal("unknown provider sizes counted", sample)
			}
		}
	}
}
