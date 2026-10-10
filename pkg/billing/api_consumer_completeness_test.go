package billing

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func usageWithErrors(minute time.Time, requests, errs int64) state.APIConsumerUsageBucket {
	return state.APIConsumerUsageBucket{WindowStart: minute, RequestCount: requests, ErrorCount: errs, BillableUnits: requests}
}

// adr: 938
func TestCheckAPIConsumerUsageCompleteness(t *testing.T) {
	h0 := allowanceStart
	h1, h2 := h0.Add(time.Hour), h0.Add(2*time.Hour)
	from, until := h0, h0.Add(3*time.Hour)
	cases := []struct {
		name      string
		usage     []state.APIConsumerUsageBucket
		telemetry []state.APIConsumerTelemetryHour
		status    string
		missing   int64
		confirmed int64
	}{
		{"verified ignores error requests on both sides",
			[]state.APIConsumerUsageBucket{usageWithErrors(h0, 10, 4), usageWithErrors(h0.Add(5*time.Minute), 2, 0)},
			[]state.APIConsumerTelemetryHour{{Hour: h0, SuccessfulRequests: 8}}, CompletenessVerified, 0, 8},
		{"telemetry saw unbilled requests",
			[]state.APIConsumerUsageBucket{usageWithErrors(h0, 5, 0)},
			[]state.APIConsumerTelemetryHour{{Hour: h0, SuccessfulRequests: 7}, {Hour: h1, SuccessfulRequests: 1}},
			CompletenessGapsDetected, 3, 5},
		{"sampled telemetry is partial, not a gap",
			[]state.APIConsumerUsageBucket{usageWithErrors(h0, 5, 0), usageWithErrors(h2, 5, 0)},
			[]state.APIConsumerTelemetryHour{{Hour: h0, SuccessfulRequests: 3}}, CompletenessPartial, 0, 3},
		{"no telemetry at all",
			[]state.APIConsumerUsageBucket{usageWithErrors(h1, 5, 0)}, nil, CompletenessUnverifiable, 0, 0},
		{"outside the checked hours is ignored",
			[]state.APIConsumerUsageBucket{usageWithErrors(until, 5, 0)},
			[]state.APIConsumerTelemetryHour{{Hour: h0.Add(-time.Hour), SuccessfulRequests: 9}}, CompletenessVerified, 0, 0},
	}
	for _, tc := range cases {
		got := CheckAPIConsumerUsageCompleteness(tc.usage, tc.telemetry, from, until)
		if got.Status != tc.status || got.MissingRequests != tc.missing || got.ConfirmedRequests != tc.confirmed {
			t.Errorf("%s: got %+v, want status %s missing %d confirmed %d", tc.name, got, tc.status, tc.missing, tc.confirmed)
		}
	}
}
