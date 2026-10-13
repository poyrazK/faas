package apidserver_test

import (
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestParseDebugTelemetryFilters_NormalizesAndMaps(t *testing.T) {
	deploymentID := uuid.New()
	consumerID := uuid.New()
	filters, err := apidserver.ParseDebugTelemetryFilters(url.Values{
		"deployment_id":  []string{"  " + deploymentID.String() + "  "},
		"status":         []string{"503"},
		"cold_boot":      []string{"false"},
		"consumer_id":    []string{consumerID.String()},
		"min_latency_ms": []string{"250"},
	})
	if err != nil {
		t.Fatalf("parseDebugTelemetryFilters() error = %v", err)
	}
	if filters.DeploymentID != deploymentID.String() || filters.Status != 503 || filters.ConsumerID != consumerID.String() || filters.MinLatencyMS != 250 {
		t.Fatalf("filters = %+v", filters)
	}
	if filters.ColdBoot == nil || *filters.ColdBoot {
		t.Fatalf("cold_boot = %v, want pointer to false", filters.ColdBoot)
	}
	if got := filters.SqlColdBootFilter(); got != 0 {
		t.Fatalf("sqlColdBootFilter() = %d, want 0", got)
	}
	if got := filters.SqlConsumerID(); got != consumerID.String() || filters.SqlConsumerAnonymous() {
		t.Fatalf("consumer SQL mapping = %q anonymous=%v", got, filters.SqlConsumerAnonymous())
	}

	anonymous, err := apidserver.ParseDebugTelemetryFilters(url.Values{"consumer_id": []string{apidserver.DebugTelemetryAnonymousConsumer}})
	if err != nil {
		t.Fatalf("anonymous filter error = %v", err)
	}
	if !anonymous.SqlConsumerAnonymous() || anonymous.SqlConsumerID() != "" {
		t.Fatalf("anonymous SQL mapping = id=%q anonymous=%v", anonymous.SqlConsumerID(), anonymous.SqlConsumerAnonymous())
	}
}

func TestParseDebugTelemetryFilters_RejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"deployment", "deployment_id", "not-a-uuid"},
		{"status low", "status", "99"},
		{"status high", "status", "600"},
		{"cold boot", "cold_boot", "maybe"},
		{"consumer", "consumer_id", "not-a-uuid"},
		{"latency negative", "min_latency_ms", "-1"},
		{"latency high", "min_latency_ms", "86400001"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := apidserver.ParseDebugTelemetryFilters(url.Values{test.key: []string{test.value}}); err == nil {
				t.Fatalf("parseDebugTelemetryFilters(%s=%q) error = nil", test.key, test.value)
			}
		})
	}
}

func TestDebugTelemetryFiltersMatchCursor(t *testing.T) {
	value := true
	filters := apidserver.DebugTelemetryFilters{
		DeploymentID: uuid.NewString(), Status: 500, ColdBoot: &value,
		ConsumerID: apidserver.DebugTelemetryAnonymousConsumer, MinLatencyMS: 100,
	}
	if !filters.Same(filters.Cursor()) {
		t.Fatal("filters should match their cursor representation")
	}
	other := filters.Cursor()
	other.MinLatencyMS++
	if filters.Same(other) {
		t.Fatal("changed latency filter should not match cursor")
	}
}
