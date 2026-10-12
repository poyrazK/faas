// spec: §12
package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// production-us hunt #9: the API availability page counted every 5xx,
// including those a tenant's app returned itself. Only a 5xx produced before
// any guest response byte is a platform error.
func TestObserveCountsOnlyPlatformProducedServerErrors(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        int
		guestAnswered bool
		want          float64
	}{
		{name: "app returned 500", status: http.StatusInternalServerError, guestAnswered: true, want: 0},
		{name: "wake failed 503", status: http.StatusServiceUnavailable, want: 1},
		{name: "budget 504", status: http.StatusGatewayTimeout, want: 1},
		{name: "client 404", status: http.StatusNotFound, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{metrics: NewMetrics()}
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			recorder := &firstByteRecorder{}
			if tc.guestAnswered {
				recorder.record(time.Now())
			}
			r = r.WithContext(WithFirstByteRecorder(r.Context(), recorder))
			h.observe(r, tc.status, "app-1", "scale", false, Target{})
			if got := testutil.ToFloat64(h.metrics.platformErrors.WithLabelValues("app-1", "scale")); got != tc.want {
				t.Fatalf("gateway_platform_errors_total = %v, want %v", got, tc.want)
			}
		})
	}
}
