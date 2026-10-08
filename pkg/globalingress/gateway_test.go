package globalingress

// adr: 784
import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixture(t *testing.T, transport http.RoundTripper, origins ...string) (*Gateway, runtimequota.IngressSnapshot, runtimequota.Signer) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	signer, err := runtimequota.NewSigner(id, priv)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Directory, IssuerID: id, Public: pub}})
	if err != nil {
		t.Fatal(err)
	}
	g, err := New("global.example.com", id, trust, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }), transport)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Microsecond)
	g.now = func() time.Time { return now }
	s := runtimequota.IngressSnapshot{DirectoryID: id, Generation: 1, IssuedAtMicros: now.UnixMicro(), ExpiresAtMicros: now.Add(api.GlobalIngressLeaseDuration).UnixMicro(), RoutingPolicy: api.GlobalRoutingPolicyAllSelected, Routes: []runtimequota.IngressRoute{{GlobalAppID: uuid.NewString()}}}
	for i, origin := range origins {
		s.Routes[0].Targets = append(s.Routes[0].Targets, runtimequota.IngressTarget{Origin: origin, Healthy: true, Eligible: true, Binding: runtimequota.Binding{CellID: uuid.NewString(), CellGeneration: 1, Region: fmt.Sprintf("region-%d", i), RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "app", APIBase: fmt.Sprintf("https://api%d.example.com", i)}})
	}
	return g, s, signer
}

func encode(t *testing.T, s runtimequota.IngressSnapshot, signer runtimequota.Signer) []byte {
	t.Helper()
	e, err := signer.SignIngressSnapshot(s)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func refresh(t *testing.T, g *Gateway, s runtimequota.IngressSnapshot, signer runtimequota.Signer) {
	t.Helper()
	if err := g.Refresh(encode(t, s, signer)); err != nil {
		t.Fatal(err)
	}
}
func request(g *Gateway, app string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://"+app+".global.example.com/path", nil))
	return w
}

func TestAllSelectedRegionsProxyRealTLSBackends(t *testing.T) {
	var counts [2]atomic.Int64
	servers := make([]*httptest.Server, 2)
	for i := range servers {
		servers[i] = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counts[i].Add(1)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			if r.URL.RequestURI() != "/path?q=a%2Fb" || string(body) != "payload" || r.Header.Get("Authorization") != "Bearer app-token" || r.Header.Get("Cookie") != "session=abc" || r.Header.Get(HopHeader) != "1" {
				t.Errorf("request changed: %s %s %v", r.URL, body, r.Header)
			}
			for _, h := range []string{api.AppIDHeader, api.PlatformTenantIDHeader, api.FlagContextHeader, api.InvocationSourceHeader, "Forwarded"} {
				if r.Header.Get(h) != "" {
					t.Errorf("reserved header crossed ingress: %s", h)
				}
			}
			if r.Header.Get("X-Forwarded-For") != "192.0.2.1" || r.Header.Get("X-Forwarded-Proto") != "http" {
				t.Errorf("untrusted forwarding survived: %v", r.Header)
			}
			w.Header().Set("Content-Security-Policy", "backend-owned")
			w.Header().Set("X-Frame-Options", "backend-owned")
			w.Header().Set(api.RequestIDHeader, "backend-id")
			w.Header().Set("X-Region", fmt.Sprint(i))
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("response"))
		}))
		t.Cleanup(servers[i].Close)
	}
	roots := x509.NewCertPool()
	for _, s := range servers {
		roots.AddCert(s.Certificate())
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	t.Cleanup(transport.CloseIdleConnections)
	g, s, signer := fixture(t, transport, servers[0].URL, servers[1].URL)
	refresh(t, g, s, signer)
	for i := 0; i < 6; i++ {
		r := httptest.NewRequest(http.MethodPost, "https://"+s.Routes[0].GlobalAppID+".global.example.com:443/path?q=a%2Fb", strings.NewReader("payload"))
		r.TLS = nil
		r.Header.Set("Authorization", "Bearer app-token")
		r.Header.Set("Cookie", "session=abc")
		r.Header.Set("Forwarded", "for=forged")
		r.Header.Set("X-Forwarded-For", "203.0.113.3")
		r.Header.Set("X-Forwarded-Proto", "https")
		for _, h := range []string{api.AppIDHeader, api.PlatformTenantIDHeader, api.FlagContextHeader, api.InvocationSourceHeader} {
			r.Header.Set(h, "forged")
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != http.StatusCreated || w.Body.String() != "response" || w.Header().Get("X-Region") != fmt.Sprint(i%2) {
			t.Fatalf("regional response: %d %v %s", w.Code, w.Header(), w.Body.String())
		}
		if w.Header().Get("X-Frame-Options") != "" || w.Header().Get(api.RequestIDHeader) != "" || w.Header().Get("Content-Security-Policy") != "backend-owned" {
			t.Fatal("regional policy escaped outer boundary")
		}
		// Renewal must not pin every first request to the first region.
		s.Generation++
		refresh(t, g, s, signer)
	}
	if counts[0].Load() != 3 || counts[1].Load() != 3 {
		t.Fatalf("distribution: %d %d", counts[0].Load(), counts[1].Load())
	}
}

func TestEligibilityExpiryWithdrawalAndHostIsolation(t *testing.T) {
	g, s, signer := fixture(t, nil, "https://a.example.com", "https://b.example.com", "https://c.example.com")
	s.Routes[0].Targets[0].Healthy = false
	s.Routes[0].Targets[1].Eligible = false
	refresh(t, g, s, signer)
	for i := 0; i < 3; i++ {
		target, found, err := g.target(s.Routes[0].GlobalAppID)
		if err != nil || !found || target.Origin != "https://c.example.com" {
			t.Fatalf("eligibility: %+v %v", target, err)
		}
	}
	if w := request(g, uuid.NewString()); w.Code != http.StatusNotFound {
		t.Fatal("unknown global app", w.Code)
	}
	for _, host := range []string{"global.example.com", "bad.global.example.com", "bad.nested.global.example.com", s.Routes[0].GlobalAppID + ".global.example.com:bad", s.Routes[0].GlobalAppID + ".global.example.com:0"} {
		r := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
		r.Host = host
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatalf("invalid global host %s: %d", host, w.Code)
		}
	}
	for _, host := range []string{"local.example.com", "a.global.example.com.attacker.test"} {
		r := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
		r.Host = host
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != http.StatusTeapot {
			t.Fatalf("local route intercepted: %s %d", host, w.Code)
		}
	}
	s.Generation++
	s.Routes[0].Targets = nil
	refresh(t, g, s, signer)
	if w := request(g, s.Routes[0].GlobalAppID); w.Code != 503 {
		t.Fatal("empty selection served", w.Code)
	}
	s.Generation++
	s.Routes = nil
	refresh(t, g, s, signer)
	if w := request(g, uuid.NewString()); w.Code != 404 {
		t.Fatal("withdrawal retained route", w.Code)
	}
	g.now = func() time.Time { return time.UnixMicro(s.ExpiresAtMicros) }
	if w := request(g, uuid.NewString()); w.Code != 503 {
		t.Fatal("expired cache served", w.Code)
	}
}

func TestInvalidRefreshQuarantinesAndNeverRewinds(t *testing.T) {
	g, s, signer := fixture(t, nil, "https://a.example.com")
	refresh(t, g, s, signer)
	s.Generation = 2
	raw := encode(t, s, signer)
	if err := g.Refresh(raw); err != nil {
		t.Fatal(err)
	}
	if err := g.Refresh([]byte("invalid")); err == nil {
		t.Fatal("malformed refresh accepted")
	}
	if _, _, err := g.target(s.Routes[0].GlobalAppID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("invalid refresh retained live routing")
	}
	if err := g.Refresh(raw); err != nil {
		t.Fatal("identical retry denied", err)
	}
	s.Routes[0].Targets[0].Healthy = false
	if err := g.Refresh(encode(t, s, signer)); err == nil {
		t.Fatal("same-generation payload changed")
	}
	s.Routes[0].Targets[0].Healthy = true
	s.Generation = 1
	if err := g.Refresh(encode(t, s, signer)); err == nil {
		t.Fatal("rollback accepted")
	}
	s.Generation = 3
	refresh(t, g, s, signer)
	s.Generation = 4
	s.IssuedAtMicros++
	s.ExpiresAtMicros++
	if err := g.Refresh(encode(t, s, signer)); err == nil {
		t.Fatal("future projection accepted")
	}
}

func TestTransportFailureNoReplayCooldownAndLoopGuard(t *testing.T) {
	var calls atomic.Int64
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("connection lost after effect")
	})
	g, s, signer := fixture(t, transport, "https://a.example.com", "https://b.example.com")
	refresh(t, g, s, signer)
	app := s.Routes[0].GlobalAppID
	if w := request(g, app); w.Code != 503 || calls.Load() != 1 {
		t.Fatal("request replayed or failure masked", calls.Load(), w.Code)
	}
	// Failure state survives directory lease renewal.
	s.Generation++
	refresh(t, g, s, signer)
	target, _, err := g.target(app)
	if err != nil || target.Origin != "https://b.example.com" {
		t.Fatal("failed target lost cooldown", target, err)
	}
	if w := request(g, app); w.Code != 503 || calls.Load() != 2 {
		t.Fatal("second region failed", w.Code, calls.Load())
	}
	if w := request(g, app); w.Code != 503 || calls.Load() != 2 {
		t.Fatal("all failed regions retried before cooldown", w.Code, calls.Load())
	}
	now := g.now()
	g.now = func() time.Time { return now.Add(api.GlobalIngressFailureCooldown) }
	if _, _, err := g.target(app); err != nil {
		t.Fatal("cooldown did not end", err)
	}
	r := httptest.NewRequest(http.MethodPost, "https://"+app+".global.example.com/v1/invocations:dispatch_batch", nil)
	r.Header.Set(HopHeader, "1")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != http.StatusLoopDetected || calls.Load() != 2 {
		t.Fatal("loop was forwarded", w.Code)
	}
}

func TestCallerCancellationDoesNotPoisonRegion(t *testing.T) {
	g, s, signer := fixture(t, roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, context.Canceled }), "https://a.example.com")
	refresh(t, g, s, signer)
	_ = request(g, s.Routes[0].GlobalAppID)
	if _, _, err := g.target(s.Routes[0].GlobalAppID); err != nil {
		t.Fatal("caller cancellation removed region", err)
	}
}

func TestConcurrentSelectionAndRenewal(t *testing.T) {
	g, s, signer := fixture(t, nil, "https://a.example.com", "https://b.example.com")
	refresh(t, g, s, signer)
	var counts [2]atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			for j := 0; j < 100; j++ {
				target, _, err := g.target(s.Routes[0].GlobalAppID)
				if err != nil {
					t.Error(err)
					return
				}
				if target.Origin == "https://a.example.com" {
					counts[0].Add(1)
				} else {
					counts[1].Add(1)
				}
			}
		})
	}
	for i := int64(2); i < 10; i++ {
		renew := s
		renew.Generation = i
		refresh(t, g, renew, signer)
	}
	wg.Wait()
	if counts[0].Load() != 1000 || counts[1].Load() != 1000 {
		t.Fatalf("unfair distribution: %d %d", counts[0].Load(), counts[1].Load())
	}
}

func backendTransport(t *testing.T, s *httptest.Server) *http.Transport {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	t.Cleanup(transport.CloseIdleConnections)
	return transport
}

func TestStreamingFlushesBeforeRegionalResponseCompletes(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: initial\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer backend.Close()
	g, s, signer := fixture(t, backendTransport(t, backend), backend.URL)
	refresh(t, g, s, signer)
	frontend := httptest.NewServer(g)
	defer frontend.Close()
	r, err := http.NewRequestWithContext(context.Background(), http.MethodGet, frontend.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = s.Routes[0].GlobalAppID + ".global.example.com"
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		t.Fatal("stream rejected", response.StatusCode)
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: initial\n" {
		t.Fatalf("stream buffered or changed: %q %v", line, err)
	}
}

func TestUpgradeTunnelAcrossRegionalTLS(t *testing.T) {
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close() }()
		if r.Header.Get("Upgrade") != "gregale-test" {
			t.Error("upgrade header lost")
			return
		}
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: gregale-test\r\n\r\n")
		if err := rw.Flush(); err != nil {
			t.Error(err)
			return
		}
		line, err := rw.ReadString('\n')
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = rw.WriteString("echo:" + line)
		if err := rw.Flush(); err != nil {
			t.Error(err)
		}
	}))
	defer backend.Close()
	g, s, signer := fixture(t, backendTransport(t, backend), backend.URL)
	refresh(t, g, s, signer)
	frontend := httptest.NewServer(g)
	defer frontend.Close()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(frontend.URL, "http://"), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequestWithContext(context.Background(), http.MethodGet, frontend.URL+"/upgrade", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = s.Routes[0].GlobalAppID + ".global.example.com"
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "gregale-test")
	if err := r.Write(conn); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 101 {
		t.Fatal("upgrade refused", response.StatusCode)
	}
	if _, err := io.WriteString(conn, "ping\n"); err != nil {
		t.Fatal(err)
	}
	if line, err := reader.ReadString('\n'); err != nil || line != "echo:ping\n" {
		t.Fatalf("upgrade tunnel: %q %v", line, err)
	}
}
