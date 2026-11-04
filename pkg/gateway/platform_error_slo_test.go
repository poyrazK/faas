package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A request whose budget expired while the app was handling it is the
// customer's slow endpoint, not a platform error: it must not feed
// gateway_platform_errors_total and the public availability SLO
// (Hunt 11 H11-34). Platform-side failures still count.
func TestPlatformErrorForSLO(t *testing.T) {
	tests := []struct {
		name   string
		status int
		write  func(http.ResponseWriter, *http.Request)
		stamp  bool // guest sent response headers
		want   bool
	}{
		{"app held the request past its budget", http.StatusGatewayTimeout, writeRequestBudgetExceededForRequest, false, false},
		{"budget spent waiting for capacity", http.StatusGatewayTimeout, func(w http.ResponseWriter, r *http.Request) {
			writeRequestBudgetExceeded(w, r, requestBudgetDetailCapacity)
		}, false, true},
		{"wake failure", http.StatusServiceUnavailable, nil, false, true},
		{"guest returned the 5xx", http.StatusInternalServerError, nil, true, false},
		{"success", http.StatusOK, nil, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &firstByteRecorder{}
			r := httptest.NewRequest(http.MethodGet, "/slow", nil)
			r = r.WithContext(WithFirstByteRecorder(r.Context(), rec))
			if tt.stamp {
				rec.record(time.Now())
			}
			if tt.write != nil {
				tt.write(httptest.NewRecorder(), r)
			}
			if got := platformErrorForSLO(r, tt.status); got != tt.want {
				t.Fatalf("platformErrorForSLO = %t, want %t", got, tt.want)
			}
		})
	}
}
