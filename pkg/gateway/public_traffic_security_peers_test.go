// adr: 570
package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

// An edge of release N runs beside N-1 compute gateways that never stamp the
// traffic security snapshot. While its peer gate says so, their unstamped
// answers pass; a stamped answer is still verified (2026-10-26, rc.295 edge in
// front of rc.294 gateways answered every success with 503).
func TestPublicTrafficSecurityAcceptsUnstampedAnswersFromN1Gateways(t *testing.T) {
	metadata := securityTestMetadata(t)
	for _, upgrade := range []bool{false, true} {
		for _, tc := range []struct {
			name     string
			required bool
			values   []string
			want     int
		}{
			{"n-1 gateway success", false, nil, 200},
			{"n-1 gateway verified anyway", false, []string{metadata}, 200},
			{"n-1 gateway malformed", false, []string{"v1.invalid"}, 503},
			{"n-1 gateway ambiguous", false, []string{metadata, metadata}, 503},
			{"every gateway stamps: missing", true, nil, 503},
			{"every gateway stamps: verified", true, []string{metadata}, 200},
		} {
			t.Run(tc.name+"/upgrade="+strconv.FormatBool(upgrade), func(t *testing.T) {
				registry := trafficrevocation.New(&gatewaySecurityStore{})
				defer registry.Close()
				compute := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					for _, value := range tc.values {
						w.Header().Add(trafficSecurityHeader, value)
					}
					w.WriteHeader(http.StatusOK)
					_, _ = io.WriteString(w, "compute body")
				}))
				defer compute.Close()
				required := tc.required
				proxy := NewInternalReverseProxy(&stubDialer{server: compute}, &url.URL{Scheme: "http", Host: "compute"}, slog.Default(), false).
					WithTrafficRevocations(registry).WithTrafficSecurityPeers(func() bool { return required })
				request := httptest.NewRequest(http.MethodGet, "http://app.test/", nil)
				if upgrade {
					request.Header.Set("Connection", "Upgrade")
					request.Header.Set("Upgrade", "websocket")
				}
				rec := httptest.NewRecorder()
				proxy.ServeHTTP(rec, request)
				if rec.Code != tc.want || rec.Header().Get(trafficSecurityHeader) != "" {
					t.Fatalf("status/headers=%d/%v body=%s", rec.Code, rec.Header(), rec.Body)
				}
				if tc.want == 503 && strings.Contains(rec.Body.String(), "compute body") {
					t.Fatal("unverified response committed")
				}
				if n, scopes := registry.Tracked(); n != 0 || scopes != 0 {
					t.Fatal("finished request retained registration")
				}
			})
		}
	}
}
