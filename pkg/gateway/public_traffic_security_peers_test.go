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
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

type fakeTrafficSecurityPeers struct {
	required   bool
	unverified atomic.Int64
}

func (f *fakeTrafficSecurityPeers) Required() bool { return f.required }
func (f *fakeTrafficSecurityPeers) Unverified()    { f.unverified.Add(1) }

// An edge of release N runs beside N-1 compute gateways that never stamp the
// traffic security snapshot and do not strip a guest's copy. While any gateway
// the edge may dial is such a peer, every answer passes unverified and a stamp
// is dropped, never trusted; once every gateway stamps, a missing or bad one
// refuses (2026-10-26: an rc.295 edge in front of rc.294 gateways answered
// every success with 503 from 10:39 to 12:14 UTC).
func TestPublicTrafficSecurityFollowsItsPeers(t *testing.T) {
	metadata := securityTestMetadata(t)
	longLived := http.Header{trafficResponseSessionHeader: {"long-lived"}}
	for _, upgrade := range []bool{false, true} {
		for _, tc := range []struct {
			name           string
			required       bool
			status         int
			header         http.Header
			values         []string
			revoked        bool
			want           int
			wantUnverified int64
		}{
			{"n-1 success", false, 200, nil, nil, false, 200, 1},
			{"n-1 redirect", false, 302, nil, nil, false, 302, 1},
			{"n-1 long-lived stream", false, 200, longLived, nil, false, 200, 1},
			{"n-1 app error", false, 404, nil, nil, false, 404, 0},
			{"n-1 stamp is dropped, not verified", false, 200, nil, []string{metadata}, false, 200, 1},
			{"n-1 forged stamp is dropped", false, 200, nil, []string{"v1.forged"}, false, 200, 1},
			{"n-1 stamp of a revoked generation is dropped", false, 200, nil, []string{metadata}, true, 200, 1},
			{"n-1 stamped app error", false, 404, nil, []string{metadata}, false, 404, 1},
			{"every gateway stamps: missing", true, 200, nil, nil, false, 503, 0},
			{"every gateway stamps: missing on a redirect", true, 302, nil, nil, false, 503, 0},
			{"every gateway stamps: missing on a stream", true, 200, longLived, nil, false, 503, 0},
			{"every gateway stamps: verified", true, 200, nil, []string{metadata}, false, 200, 0},
			{"every gateway stamps: revoked generation", true, 200, nil, []string{metadata}, true, 403, 0},
			{"every gateway stamps: unstamped app error", true, 404, nil, nil, false, 404, 0},
		} {
			t.Run(tc.name+"/upgrade="+strconv.FormatBool(upgrade), func(t *testing.T) {
				store := &gatewaySecurityStore{}
				if tc.revoked {
					store.set(trafficrevocation.Scope{Kind: "app", ID: securityTestApp}, 1, true)
				}
				registry := trafficrevocation.New(store)
				defer registry.Close()
				compute := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					for name, values := range tc.header {
						w.Header()[name] = values
					}
					for _, value := range tc.values {
						w.Header().Add(trafficSecurityHeader, value)
					}
					if tc.status == http.StatusFound {
						w.Header().Set("Location", "/elsewhere")
					}
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, "compute body")
				}))
				defer compute.Close()
				peers := &fakeTrafficSecurityPeers{required: tc.required}
				proxy := NewInternalReverseProxy(&stubDialer{server: compute}, &url.URL{Scheme: "http", Host: "compute"}, slog.Default(), false).
					WithTrafficRevocations(registry).WithTrafficSecurityPeers(peers)
				request := httptest.NewRequest(http.MethodGet, "http://app.test/", nil)
				if upgrade {
					request.Header.Set("Connection", "Upgrade")
					request.Header.Set("Upgrade", "websocket")
				}
				rec := httptest.NewRecorder()
				proxy.ServeHTTP(rec, request)
				if rec.Code != tc.want {
					t.Fatalf("status=%d, want %d; body=%s", rec.Code, tc.want, rec.Body)
				}
				if rec.Header().Get(trafficSecurityHeader) != "" {
					t.Fatalf("the snapshot leaked to the client: %v", rec.Header())
				}
				if tc.want >= 500 && strings.Contains(rec.Body.String(), "compute body") {
					t.Fatal("unverified response committed")
				}
				if got := peers.unverified.Load(); got != tc.wantUnverified {
					t.Fatalf("unverified answers = %d, want %d", got, tc.wantUnverified)
				}
				if n, scopes := registry.Tracked(); n != 0 || scopes != 0 {
					t.Fatal("finished request retained registration")
				}
			})
		}
	}
}

// TestPublicProxyServesAPlainN1Answer is the public hop's release-skew guard
// (docs/release-compatibility.md): an answer from an N-1 compute gateway
// carries no platform headers at all, and the public edge must still serve it
// while its peer gate says a gateway it may dial predates them. Any new
// header the public hop starts to require must keep this passing for one
// release.
func TestPublicProxyServesAPlainN1Answer(t *testing.T) {
	registry := trafficrevocation.New(&gatewaySecurityStore{})
	defer registry.Close()
	compute := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "hello from an older gateway")
	}))
	defer compute.Close()
	proxy := NewInternalReverseProxy(&stubDialer{server: compute}, &url.URL{Scheme: "http", Host: "compute"}, slog.Default(), false).
		WithTrafficRevocations(registry).WithTrafficSecurityPeers(&fakeTrafficSecurityPeers{})
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://app.test/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "hello from an older gateway" {
		t.Fatalf("status=%d body=%q, want the N-1 answer served", rec.Code, rec.Body)
	}
}
