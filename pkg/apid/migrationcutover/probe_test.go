package migrationcutover

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
)

func TestEdgePolicyRejectsArbitraryPublicOriginAndReboundAnswers(t *testing.T) {
	lookups := 0
	lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
		lookups++
		if host != "edge.example.test" {
			t.Errorf("untrusted edge target: %s", host)
		}
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	public := func(ip net.IP) bool { return !ip.IsLoopback() }
	for _, configured := range [][]netip.Addr{nil, {netip.MustParseAddr("8.8.8.8")}} {
		allowed, err := EdgeAddressPolicy(t.Context(), "edge.example.test", configured, lookup, public)
		if err != nil {
			t.Fatal(err)
		}
		if !allowed(net.ParseIP("8.8.8.8")) || allowed(net.ParseIP("9.9.9.9")) || allowed(net.ParseIP("127.0.0.1")) {
			t.Fatal("arbitrary public origin or private origin trusted")
		}
	}
	if lookups != 1 {
		t.Fatalf("configured address used customer DNS: %d", lookups)
	}
}

func TestPublicClientPinsDNSAndRejectsMixedPrivateAnswers(t *testing.T) {
	for _, private := range []bool{true, false} {
		t.Run(map[bool]string{true: "mixed_private", false: "pinned"}[private], func(t *testing.T) {
			lookups, dials := 0, 0
			lookup := func(context.Context, string) ([]net.IPAddr, error) {
				lookups++
				out := []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}
				if private {
					out = append(out, net.IPAddr{IP: net.ParseIP("127.0.0.1")})
				}
				return out, nil
			}
			client := PublicClient("api.example.test", lookup, func(ip net.IP) bool { return !ip.IsLoopback() }, func(_ context.Context, _ string, address string) (net.Conn, error) {
				dials++
				if address != "8.8.8.8:443" {
					t.Errorf("unpinned dial: %s", address)
				}
				return nil, context.Canceled
			})
			transport := client.Transport.(*http.Transport)
			if transport.Proxy != nil || transport.TLSClientConfig.InsecureSkipVerify {
				t.Fatal("proxy or insecure TLS enabled")
			}
			if conn, err := transport.DialContext(t.Context(), "tcp", "api.example.test:443"); err == nil || conn != nil {
				t.Fatal("unexpected connection")
			}
			want := 1
			if private {
				want = 0
			}
			if lookups != 1 || dials != want {
				t.Fatalf("lookups=%d dials=%d", lookups, dials)
			}
			if _, err := transport.DialContext(t.Context(), "tcp", "localhost:443"); err == nil {
				t.Fatal("wrong host accepted")
			}
		})
	}
}

func TestTrustedOldProviderAndRedirectCannotPassDeploymentProof(t *testing.T) {
	for _, mode := range []string{"old_provider", "redirect", "gateway"} {
		t.Run(mode, func(t *testing.T) {
			dep := uuid.NewString()
			var calls atomic.Int32
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if mode == "redirect" {
					http.Redirect(w, r, "https://localhost/private", http.StatusFound)
					return
				}
				if mode == "gateway" {
					w.Header().Set(apihostingreceipt.ServedDeploymentHeader, dep)
					w.Header().Set(apihostingreceipt.ServedResponseHeader, apihostingreceipt.CandidateResponseProof(dep, r.Header.Get(apihostingreceipt.PlatformSmokeTokenHeader)))
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			client := srv.Client()
			transport := client.Transport.(*http.Transport)
			// Trust httptest's certificate, with its example.com SAN, while
			// routing this hermetic test to the local TLS listener.
			transport.TLSClientConfig = &tls.Config{RootCAs: transport.TLSClientConfig.RootCAs, ServerName: "example.com", MinVersion: tls.VersionTLS12}
			target := strings.TrimPrefix(srv.URL, "https://")
			transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, target)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
			defer cancel()
			checks := ProbeRoute(ctx, api.MigrationCutover{App: "cutover", DeploymentID: dep, SmokePath: "/ready", Request: api.CreateMigrationCutoverRequest{Hostname: "example.com"}}, client, func(context.Context, string, string, time.Time) error { return nil })
			if checks[0].Status != "passed" {
				t.Fatalf("trusted TLS not detected: %+v", checks)
			}
			want := "pending"
			if mode == "gateway" {
				want = "passed"
			}
			if checks[1].Status != want || calls.Load() != 1 {
				t.Fatalf("wrong provider/redirect verdict: %+v calls=%d", checks, calls.Load())
			}
		})
	}
}
