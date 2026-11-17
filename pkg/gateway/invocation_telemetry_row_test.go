package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A delivered cron or queue invocation's telemetry row names its source as
// the client family, so HTTP logs tell it apart from a caller's request; a
// service call's row keeps the unknown family.
func TestServiceRequestTelemetryRow_InvocationSourceFamily(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/report", nil)
	base := ServiceRequestObservation{
		AccountID: uuid.NewString(), ReceivedAt: time.Now(), LatencyMS: 12, Status: http.StatusNotFound,
		Target: Target{AppID: uuid.NewString(), DeploymentID: uuid.NewString(), InstanceID: "i-1"},
	}
	tests := []struct {
		source, want string
	}{
		{"", telemetryUnknownDimension},
		{"cron", "gregale-cron"},
		{"queue", "gregale-queue"},
	}
	for _, tt := range tests {
		o := base
		o.InvocationSource = tt.source
		row, ok := serviceRequestTelemetryRow(r, o)
		if !ok || row.UAFamily != tt.want || row.Status != http.StatusNotFound || !row.UsageOutboxed {
			t.Fatalf("source %q: row = %+v, ok=%v; want family %q, no usage", tt.source, row, ok, tt.want)
		}
	}
}
