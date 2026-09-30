// adr: 375
package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	securityTestAccount    = "10000000-0000-0000-0000-000000000001"
	securityTestApp        = "20000000-0000-0000-0000-000000000002"
	securityTestDeployment = "30000000-0000-0000-0000-000000000003"
)

func securityTestHandler(t *testing.T, registry *trafficrevocation.Registry) (*Handler, *fakeBackend) {
	t.Helper()
	h, backend, _ := newTestHandler(t)
	backend.app.ID, backend.app.AccountID = securityTestApp, securityTestAccount
	backend.setLegacyHot()
	backend.targets[0].AppID, backend.targets[0].DeploymentID = securityTestApp, securityTestDeployment
	h.WithTrafficRevocations(registry)
	return h, backend
}

func TestPublicTrafficRevocationInterruptsBlockedWrite(t *testing.T) {
	for _, change := range []string{"revoke", "missed-revoke-release", "store-outage"} {
		for _, long := range []bool{false, true} {
			for _, h2 := range []bool{false, true} {
				t.Run(change+"/long="+strconv.FormatBool(long)+"/h2="+strconv.FormatBool(h2), func(t *testing.T) {
					store := &gatewaySecurityStore{}
					computeRegistry, publicRegistry := trafficrevocation.New(store), trafficrevocation.New(store)
					defer computeRegistry.Close()
					defer publicRegistry.Close()
					h, backend := securityTestHandler(t, computeRegistry)
					if long {
						backend.app.StreamingEnabled = true
						h.WithStreamingEnabled(true)
					}
					fixture := &deadlineFloodServer{finished: make(chan error, 1)}
					nodes := singleClientLookup{cli: newDeadlineForwardClient(t, fixture)}
					log := slog.New(slog.NewTextHandler(io.Discard, nil))
					h.WithForwarding(func(target Target) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fwdOnceWithEvents(w, r, nodes, log, target, nil) })
					})
					computeDone, publicDone := make(chan struct{}), make(chan struct{})
					compute := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(computeDone); h.ServeHTTP(w, r) }))
					t.Cleanup(compute.Close)
					proxy := NewInternalReverseProxy(&stubDialer{server: compute}, &url.URL{Scheme: "http", Host: "compute"}, log, false).WithTrafficRevocations(publicRegistry)
					budgetDuration := 3 * time.Second
					if long {
						budgetDuration = 80 * time.Millisecond
					}
					budget := reqbudget.MiddlewareConfig{Default: budgetDuration, Max: 3 * time.Second, Route: "forward", Endpoint: "test"}
					stack := httpsec.Static(budget.Middleware(otelhttp.NewHandler(proxy, "public")))
					public := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(publicDone); stack.ServeHTTP(w, r) }))
					public.EnableHTTP2 = h2
					if h2 {
						public.StartTLS()
					} else {
						public.Start()
					}
					t.Cleanup(public.Close)
					resp, closeClient := openSlowResponseReader(t, public, backend.host, h2, false)
					defer func() { _ = resp.Body.Close() }()
					defer closeClient()
					if resp.StatusCode != http.StatusOK || resp.Header.Get(trafficSecurityHeader) != "" {
						t.Fatalf("status/metadata=%d/%q", resp.StatusCode, resp.Header.Get(trafficSecurityHeader))
					}
					if exchanges, scopes := publicRegistry.Tracked(); exchanges != 1 || scopes != 3 {
						t.Fatalf("public ownership=%d/%d", exchanges, scopes)
					}
					time.Sleep(100 * time.Millisecond) // Fill the open, unread client socket/window.
					select {
					case <-publicDone:
						t.Fatal("response ended before security change")
					default:
					}
					if change == "store-outage" {
						store.mu.Lock()
						store.err = errors.New("verification offline")
						store.mu.Unlock()
					} else {
						revision := int64(1)
						if change == "missed-revoke-release" {
							revision = 2
						}
						store.set(trafficrevocation.Scope{Kind: "deployment", ID: securityTestDeployment}, revision, change == "revoke")
					}
					// Only public repairs security. It must cancel transport ownership
					// while blocked writing, without reading compute's eventual reset.
					if long && h2 && change == "missed-revoke-release" {
						go publicRegistry.Run(t.Context())
					} else {
						_ = publicRegistry.Refresh(t.Context())
					}
					for name, done := range map[string]<-chan struct{}{"public": publicDone, "compute": computeDone} {
						select {
						case <-done:
						case <-time.After(2 * time.Second):
							t.Fatalf("%s retained blocked response before client close", name)
						}
					}
					select {
					case err := <-fixture.finished:
						if !errors.Is(err, context.Canceled) {
							t.Fatalf("RPC cancellation=%v", err)
						}
					case <-time.After(time.Second):
						t.Fatal("public cancellation retained upstream RPC")
					}
					if sent := fixture.sent.Load(); sent <= 0 || sent >= slowReaderBodySize {
						t.Fatalf("fixture missed backpressure: %d", sent)
					}
					for _, registry := range []*trafficrevocation.Registry{computeRegistry, publicRegistry} {
						if n, scopes := registry.Tracked(); n != 0 || scopes != 0 {
							t.Fatalf("cleanup leaked %d/%d", n, scopes)
						}
					}
				})
			}
		}
	}
}

type securityHandoffTransport struct {
	base  http.RoundTripper
	after func(*http.Response)
}

func (t securityHandoffTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err == nil {
		t.after(resp)
	}
	return resp, err
}

func TestPublicTrafficRevocationRefusesMissedPairAtHandoffAndRecovers(t *testing.T) {
	store := &gatewaySecurityStore{}
	computeRegistry, publicRegistry := trafficrevocation.New(store), trafficrevocation.New(store)
	defer computeRegistry.Close()
	defer publicRegistry.Close()
	h, backend := securityTestHandler(t, computeRegistry)
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	})
	compute := httptest.NewServer(h)
	defer compute.Close()
	proxy := NewInternalReverseProxy(&stubDialer{server: compute}, &url.URL{Scheme: "http", Host: "compute"}, slog.Default(), false).WithTrafficRevocations(publicRegistry)
	var handoffs atomic.Int32
	proxy.Transport = securityHandoffTransport{base: proxy.Transport, after: func(resp *http.Response) {
		if handoffs.Add(1) == 1 {
			store.set(trafficrevocation.Scope{Kind: "app", ID: securityTestApp}, 2, false)
		}
	}}
	for _, want := range []int{http.StatusForbidden, http.StatusNoContent} {
		rec := httptest.NewRecorder()
		proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
		if rec.Code != want || rec.Header().Get(trafficSecurityHeader) != "" {
			t.Fatalf("handoff response=%d/%s", rec.Code, rec.Body)
		}
		if want == http.StatusForbidden && !strings.Contains(rec.Body.String(), api.CodeTrafficRevoked) {
			t.Fatal("missing handoff refusal evidence")
		}
		if n, scopes := publicRegistry.Tracked(); n != 0 || scopes != 0 {
			t.Fatalf("handoff retained %d/%d", n, scopes)
		}
	}
}

func securityTestMetadata(t *testing.T) string {
	t.Helper()
	value, err := encodeTrafficSecurity(map[trafficrevocation.Scope]trafficrevocation.State{
		{Kind: "account", ID: securityTestAccount}:       {},
		{Kind: "app", ID: securityTestApp}:               {},
		{Kind: "deployment", ID: securityTestDeployment}: {},
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestPublicTrafficSecurityManagedRealtimeRetainsSeparateOwner(t *testing.T) {
	registry := trafficrevocation.New(&gatewaySecurityStore{})
	defer registry.Close()
	h := NewHandlerWith(nil, NewMetrics(), nil)
	h.WithManagedRealtime(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	compute := httptest.NewServer(h)
	defer compute.Close()
	proxy := NewInternalReverseProxy(&stubDialer{server: compute}, &url.URL{Scheme: "http", Host: "compute"}, slog.Default(), false).WithTrafficRevocations(registry)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://app.test"+realtime.ManagedPathPrefix+"notifications", nil))
	if rec.Code != http.StatusNoContent || rec.Header().Get(trafficSecurityHeader) != "" {
		t.Fatalf("managed owner response=%d/%v", rec.Code, rec.Header())
	}
	if n, scopes := registry.Tracked(); n != 0 || scopes != 0 {
		t.Fatal("managed connection acquired an application scope")
	}
}

func TestPublicTrafficSecurityMetadataRefusals(t *testing.T) {
	metadata := securityTestMetadata(t)
	for _, upgrade := range []bool{false, true} {
		for _, tc := range []struct {
			name         string
			values       []string
			status, want int
		}{
			{"verified", []string{metadata}, 200, 200},
			{"missing", nil, 200, 503},
			{"ambiguous", []string{metadata, metadata}, 200, 503},
			{"malformed", []string{"v1.invalid"}, 200, 503},
			{"oversized", []string{strings.Repeat("a", api.TrafficSecurityMaxHeaderBytes+1)}, 200, 503},
			{"excluded-on-app-path", []string{trafficSecurityRealtime}, 200, 503},
			{"pre-admission-error", nil, 403, 403},
			{"unverified-redirect", nil, 302, 503},
		} {
			t.Run(tc.name+"/upgrade="+strconv.FormatBool(upgrade), func(t *testing.T) {
				registry := trafficrevocation.New(&gatewaySecurityStore{})
				defer registry.Close()
				compute := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					for _, value := range tc.values {
						w.Header().Add(trafficSecurityHeader, value)
					}
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, "compute body")
				}))
				defer compute.Close()
				proxy := NewInternalReverseProxy(&stubDialer{server: compute}, &url.URL{Scheme: "http", Host: "compute"}, slog.Default(), false).WithTrafficRevocations(registry)
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
					t.Fatal("metadata refusal retained registration")
				}
			})
		}
	}
}

func TestComputeSecurityMetadataRejectsGuestAndLateTrailerForgery(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, announced := range []bool{false, true} {
			t.Run("h2="+strconv.FormatBool(h2)+"/announced="+strconv.FormatBool(announced), func(t *testing.T) {
				store := &gatewaySecurityStore{}
				computeRegistry, publicRegistry := trafficrevocation.New(store), trafficrevocation.New(store)
				defer computeRegistry.Close()
				defer publicRegistry.Close()
				h, backend := securityTestHandler(t, computeRegistry)
				h.WithForwarding(func(Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set(trafficSecurityHeader, trafficSecurityRealtime)
						w.Header().Set("Trailer", "grpc-status")
						if announced {
							w.Header().Add("Trailer", trafficSecurityHeader)
						}
						w.WriteHeader(http.StatusOK)
						_, _ = io.WriteString(w, "body")
						name := http.TrailerPrefix + trafficSecurityHeader
						if announced {
							name = trafficSecurityHeader
						}
						w.Header().Set(name, trafficSecurityRealtime)
						w.Header().Set("Grpc-Status", "0")
					})
				})
				compute := httptest.NewUnstartedServer(h)
				compute.Config.Protocols = new(http.Protocols)
				compute.Config.Protocols.SetHTTP1(true)
				compute.Config.Protocols.SetUnencryptedHTTP2(true)
				compute.Start()
				defer compute.Close()
				proxy := NewInternalReverseProxy(&stubDialer{server: compute}, &url.URL{Scheme: "http", Host: "compute"}, slog.Default(), h2).WithTrafficRevocations(publicRegistry)
				public := httptest.NewServer(proxy)
				defer public.Close()
				request, err := http.NewRequest(http.MethodGet, public.URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				request.Host = backend.host
				resp, err := public.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil || string(body) != "body" || resp.StatusCode != 200 || resp.Header.Get(trafficSecurityHeader) != "" || resp.Trailer.Get(trafficSecurityHeader) != "" || resp.Trailer.Get("Grpc-Status") != "0" {
					t.Fatalf("security metadata/body=%v/%v/%q/%v", resp.Header, resp.Trailer, body, err)
				}
			})
		}
	}
}

func TestPublicTrafficRevocationClosesBothUpgradeDirections(t *testing.T) {
	for _, direction := range []string{"idle", "backend-to-client", "client-to-backend"} {
		for _, outage := range []bool{false, true} {
			t.Run(direction+"/outage="+strconv.FormatBool(outage), func(t *testing.T) {
				store := &gatewaySecurityStore{}
				registry := trafficrevocation.New(store)
				defer registry.Close()
				metadata := securityTestMetadata(t)
				backendDone, publicDone, resumeRead := make(chan struct{}), make(chan struct{}), make(chan struct{})
				releaseRead := sync.OnceFunc(func() { close(resumeRead) })
				defer releaseRead()
				var backendBytes atomic.Int64
				compute := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer close(backendDone)
					conn, buffer, err := http.NewResponseController(w).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
					_, _ = fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n%s: %s\r\n\r\n", trafficSecurityHeader, metadata)
					if err := buffer.Flush(); err != nil {
						t.Error(err)
						return
					}
					if direction == "backend-to-client" {
						floodUpgradeSocket(conn, &backendBytes)
						return
					}
					if direction == "client-to-backend" {
						<-resumeRead
					}
					_, _ = io.Copy(io.Discard, buffer)
				}))
				defer compute.Close()
				proxy := NewInternalReverseProxy(&stubDialer{server: compute}, &url.URL{Scheme: "http", Host: "compute"}, slog.Default(), true).WithTrafficRevocations(registry)
				budget := reqbudget.MiddlewareConfig{Default: 80 * time.Millisecond, Max: time.Second, Route: "forward", Endpoint: "test"}
				stack := httpsec.Static(budget.Middleware(otelhttp.NewHandler(proxy, "public")))
				public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(publicDone); stack.ServeHTTP(w, r) }))
				defer public.Close()
				conn, err := net.DialTimeout("tcp", public.Listener.Addr().String(), time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				if tcp, ok := conn.(*net.TCPConn); ok {
					_ = tcp.SetReadBuffer(1024)
				}
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				_, _ = fmt.Fprint(conn, "GET /socket HTTP/1.1\r\nHost: app.test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
				resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = resp.Body.Close() }()
				if resp.StatusCode != 101 || resp.Header.Get(trafficSecurityHeader) != "" {
					t.Fatalf("upgrade=%d/%v", resp.StatusCode, resp.Header)
				}
				clientDone := make(chan struct{})
				var clientBytes atomic.Int64
				if direction == "client-to-backend" {
					go func() { defer close(clientDone); floodUpgradeSocket(conn, &clientBytes) }()
				}
				time.Sleep(150 * time.Millisecond) // Past handshake budget, both sockets stay open.
				select {
				case <-publicDone:
					t.Fatal("upgrade retained handshake deadline")
				default:
				}
				if outage {
					store.mu.Lock()
					store.err = errors.New("store unavailable")
					store.mu.Unlock()
				} else {
					store.set(trafficrevocation.Scope{Kind: "app", ID: securityTestApp}, 1, true)
				}
				_ = registry.Refresh(t.Context())
				select {
				case <-publicDone:
				case <-time.After(time.Second):
					t.Fatal("revocation retained public upgrade/copy ownership")
				}
				if direction == "client-to-backend" {
					releaseRead()
					select {
					case <-clientDone:
					case <-time.After(time.Second):
						t.Fatal("revocation retained blocked client writer")
					}
					if sent := clientBytes.Load(); sent <= 0 || sent >= slowReaderBodySize {
						t.Fatalf("no client backpressure: %d", sent)
					}
				}
				select {
				case <-backendDone:
				case <-time.After(time.Second):
					t.Fatal("revocation retained backend socket")
				}
				if direction == "backend-to-client" {
					if sent := backendBytes.Load(); sent <= 0 || sent >= slowReaderBodySize {
						t.Fatalf("no backend backpressure: %d", sent)
					}
				}
				if n, scopes := registry.Tracked(); n != 0 || scopes != 0 {
					t.Fatalf("upgrade retained %d/%d", n, scopes)
				}
			})
		}
	}
}

func floodUpgradeSocket(conn net.Conn, sent *atomic.Int64) {
	chunk := make([]byte, 32<<10)
	for sent.Load() < slowReaderBodySize {
		n, err := conn.Write(chunk)
		sent.Add(int64(n))
		if err != nil {
			return
		}
	}
}

func TestPublicTrafficUpgradeRejectsInvalidHandshakeBeforeHijack(t *testing.T) {
	for _, tc := range []struct{ name, connection, protocol string }{
		{"missing-connection-token", "", "websocket"},
		{"different-protocol", "Connection: Upgrade\r\n", "other-protocol"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := trafficrevocation.New(&gatewaySecurityStore{})
			defer registry.Close()
			metadata := securityTestMetadata(t)
			backendDone, publicDone := make(chan struct{}), make(chan struct{})
			compute := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(backendDone)
				conn, buffer, err := http.NewResponseController(w).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				_, _ = fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\n%sUpgrade: %s\r\n%s: %s\r\n\r\n", tc.connection, tc.protocol, trafficSecurityHeader, metadata)
				_ = buffer.Flush()
				_, _ = io.Copy(io.Discard, buffer)
			}))
			defer compute.Close()
			proxy := NewInternalReverseProxy(&stubDialer{server: compute}, &url.URL{Scheme: "http", Host: "compute"}, slog.Default(), false).WithTrafficRevocations(registry)
			public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(publicDone); proxy.ServeHTTP(w, r) }))
			defer public.Close()
			request, err := http.NewRequest(http.MethodGet, public.URL+"/socket", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Connection", "Upgrade")
			request.Header.Set("Upgrade", "websocket")
			resp, err := public.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadGateway || resp.Header.Get(trafficSecurityHeader) != "" {
				t.Fatalf("invalid handshake committed: %d/%v", resp.StatusCode, resp.Header)
			}
			for name, done := range map[string]<-chan struct{}{"public": publicDone, "backend": backendDone} {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatalf("%s retained refused upgrade", name)
				}
			}
			if n, scopes := registry.Tracked(); n != 0 || scopes != 0 {
				t.Fatal("invalid handshake retained security ownership")
			}
		})
	}
}
