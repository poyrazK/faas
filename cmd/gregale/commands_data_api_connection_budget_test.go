package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func connectionBudgetFixture() api.DataAPIConnectionBudgets {
	now := time.Now().UTC()
	capacity := int64(20)
	return api.DataAPIConnectionBudgets{Version: 1, App: "notes", State: "within_budget", Databases: []api.ManagedPostgresConnectionBudget{{Version: 1, DatabaseID: uuid.NewString(), DatabaseName: "primary", State: "within_budget", CheckedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(api.DataAPIConnectionBudgetMaxAge).Format(time.RFC3339Nano), Model: "canonical_runtime_pools", Reasons: []string{}, Demand: api.ConnectionBudgetDemand{DataAPIResidentEstimate: 2, DataAPIWakeEstimate: 6, ControlHeadroom: 1, ModeledPeakConnections: 7}, Capacity: api.ConnectionBudgetCapacity{State: "verified", UsableConnections: &capacity}}}}
}

func TestDataAPIConnectionBudgetRuntimeCoverageConsistency(t *testing.T) {
	b := connectionBudgetFixture().Databases[0]
	b.Inventory.DataAPIConsumers, b.Inventory.ResidentInstances, b.Inventory.ParkedInstances = 1, 2, 1
	b.Demand.DataAPIResidentEstimate = 4
	b.Demand.RuntimeObservations = &api.ConnectionBudgetRuntimeObservations{State: "partial", Source: "workload_report", ObservedInstances: 1, UnobservedInstances: 1, ParkedInstances: 1, ConfiguredConnections: 2, CheckedAt: b.CheckedAt, ExpiresAt: b.ExpiresAt}
	now := time.Now()
	if !validDataAPIConnectionBudget(b, now) {
		t.Fatal("valid coverage rejected")
	}
	for _, change := range []func(*api.ConnectionBudgetRuntimeObservations){
		func(r *api.ConnectionBudgetRuntimeObservations) { r.Source = "private-provider" },
		func(r *api.ConnectionBudgetRuntimeObservations) { r.State = "observed" },
		func(r *api.ConnectionBudgetRuntimeObservations) { r.ConfiguredConnections = 999 },
		func(r *api.ConnectionBudgetRuntimeObservations) { r.UnobservedInstances = 0 },
		func(r *api.ConnectionBudgetRuntimeObservations) { r.ObservedInstances = -1 },
		func(r *api.ConnectionBudgetRuntimeObservations) {
			r.CheckedAt = now.Add(time.Hour).Format(time.RFC3339Nano)
		},
		func(r *api.ConnectionBudgetRuntimeObservations) {
			r.ExpiresAt = now.Add(-time.Second).Format(time.RFC3339Nano)
		},
		func(r *api.ConnectionBudgetRuntimeObservations) { r.CheckedAt = "" },
	} {
		invalid := b
		coverage := *b.Demand.RuntimeObservations
		change(&coverage)
		invalid.Demand.RuntimeObservations = &coverage
		if validDataAPIConnectionBudget(invalid, now) {
			t.Fatal("invalid coverage accepted", coverage)
		}
	}
}

func TestDataAPIConnectionBudgetCapacityFreshnessAndZeroCeiling(t *testing.T) {
	now := time.Now().UTC()
	value := connectionBudgetFixture().Databases[0]
	zero := int64(0)
	value.State = "over_budget"
	value.Capacity.UsableConnections = &zero
	value.Capacity.Source = "provider_api"
	value.Capacity.CheckedAt = value.CheckedAt
	value.Capacity.ExpiresAt = value.ExpiresAt
	if !validDataAPIConnectionBudget(value, now.Add(time.Second)) {
		t.Fatal("verified zero ceiling rejected")
	}
	for _, change := range []func(*api.ManagedPostgresConnectionBudget){
		func(b *api.ManagedPostgresConnectionBudget) { b.Capacity.Source = "private-provider" },
		func(b *api.ManagedPostgresConnectionBudget) {
			b.Capacity.CheckedAt = now.Add(time.Minute).Format(time.RFC3339Nano)
		},
		func(b *api.ManagedPostgresConnectionBudget) {
			b.Capacity.ExpiresAt = now.Add(-time.Second).Format(time.RFC3339Nano)
		},
		func(b *api.ManagedPostgresConnectionBudget) {
			b.Capacity.ExpiresAt = now.Add(time.Hour).Format(time.RFC3339Nano)
		},
		func(b *api.ManagedPostgresConnectionBudget) { b.Capacity.Source = "" },
	} {
		invalid := value
		change(&invalid)
		if validDataAPIConnectionBudget(invalid, now.Add(time.Second)) {
			t.Fatal("invalid capacity metadata accepted", invalid.Capacity)
		}
	}
}

func TestDataAPIConnectionBudgetReadRejectsUnsafeAndInconsistentMetadata(t *testing.T) {
	for _, mode := range []string{"within", "over", "unknown", "not-applicable", "missing", "missing-count", "wrong-app", "private-name", "private-reason", "stale", "future", "negative", "inconsistent", "unverified-green", "enforced", "duplicate", "truncated-green"} {
		t.Run(mode, func(t *testing.T) {
			value := connectionBudgetFixture()
			b := &value.Databases[0]
			switch mode {
			case "over":
				*b.Capacity.UsableConnections = 6
				b.State = "over_budget"
				value.State = "over_budget"
			case "unknown":
				b.State = "unknown"
				b.Capacity = api.ConnectionBudgetCapacity{State: "unknown"}
				b.Reasons = []string{"capacity_unverified"}
				value.State = "unknown"
			case "not-applicable":
				value.Databases = []api.ManagedPostgresConnectionBudget{}
				value.State = "not_applicable"
			case "wrong-app":
				value.App = "other"
			case "private-name":
				b.DatabaseName = "postgres://private-password"
			case "private-reason":
				b.Reasons = []string{"private-row-value"}
			case "stale":
				b.ExpiresAt = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
			case "future":
				b.CheckedAt = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
			case "negative":
				b.Inventory.ActiveTasks = -1
			case "inconsistent":
				b.Demand.ModeledPeakConnections++
			case "unverified-green":
				b.Capacity = api.ConnectionBudgetCapacity{State: "unknown"}
			case "enforced":
				b.Enforced = true
			case "duplicate":
				value.Databases = append(value.Databases, *b)
			case "truncated-green":
				value.Truncated = true
			}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/v1/apps/notes/data-api/connection-budget" || r.Header.Get("Authorization") != "Bearer owner-token" {
					t.Error("unexpected request", r.Method, r.URL.Path)
				}
				if mode == "missing" {
					http.Error(w, "private-provider-error", http.StatusNotFound)
					return
				}
				if mode == "missing-count" {
					data, _ := json.Marshal(value)
					var object map[string]any
					_ = json.Unmarshal(data, &object)
					delete(object["databases"].([]any)[0].(map[string]any)["demand"].(map[string]any), "data_api_wake_estimate")
					_ = json.NewEncoder(w).Encode(object)
					return
				}
				if err := json.NewEncoder(w).Encode(value); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()
			got := readDataAPIConnectionBudget(context.Background(), api.NewClient(srv.URL, "owner-token"), "notes")
			want := map[string]string{"within": "within_budget", "over": "over_budget", "not-applicable": "not_applicable"}[mode]
			if want == "" {
				want = "unknown"
			}
			if got.State != want || calls != 1 {
				t.Fatal(got, calls)
			}
			bytes, _ := json.Marshal(got)
			if strings.Contains(string(bytes), "private-") {
				t.Fatal("unsafe metadata escaped")
			}
			r := dataAPIHealthReport{Status: "healthy"}
			attachDataAPIConnectionBudget(&r, got)
			if (want == "unknown") != r.Partial || slices.Contains(r.CurrentFindings, "connection_budget_exceeded") != (want == "over_budget") {
				t.Fatal(r)
			}
			if want == "unknown" && r.Status != "healthy" {
				t.Fatal("unknown capacity became an outage")
			}
		})
	}
}

func TestDataAPIConnectionBudgetHumanOutputShowsPlanningAndUnknownCapacity(t *testing.T) {
	stdout, _, restore := swapIO(t)
	defer restore()
	b := connectionBudgetFixture()
	b.State = "unknown"
	b.Databases[0].State = "unknown"
	b.Databases[0].Capacity = api.ConnectionBudgetCapacity{State: "unknown"}
	if err := writeDataAPIConnectionBudget(&b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "planning only") || !strings.Contains(stdout.String(), "verified usable capacity unknown") {
		t.Fatal(fmt.Sprint(stdout))
	}
}
