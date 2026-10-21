package hosting

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func pilotTestSocket(t *testing.T, handler http.Handler) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "hp-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socket := filepath.Join(root, "relay.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return socket
}

func TestPilotTransportUsesOnlyPrivateUnixSocket(t *testing.T) {
	var requests atomic.Int64
	socket := pilotTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.Host != "hosting-pilot.local" || r.URL.RequestURI() != "/client/v4/accounts/abc/analytics_engine/sql" || r.Header.Get("Authorization") != "Bearer private-test-token" || string(body) != "fixed query" {
			t.Error("relay changed request identity or body")
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	transport, err := newPilotTransport(socket)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.cloudflare.com/client/v4/accounts/abc/analytics_engine/sql", strings.NewReader("fixed query"))
	request.Header.Set("Authorization", "Bearer private-test-token")
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if request.URL.Scheme != "https" || request.URL.Host != "api.cloudflare.com" {
		t.Fatal("mutated original request")
	}
	for _, target := range []string{"http://api.cloudflare.com/client/v4/a", "https://evil.example/client/v4/a", "https://api.cloudflare.com:443/client/v4/a", "https://api.cloudflare.com/other"} {
		bad, _ := http.NewRequest(http.MethodGet, target, nil)
		reply, err := transport.RoundTrip(bad)
		if reply != nil {
			_ = reply.Body.Close()
		}
		if err == nil {
			t.Fatalf("accepted non-provider request %s", target)
		}
	}
	if requests.Load() != 1 {
		t.Fatal("unsafe request reached relay")
	}
	if err := os.Chmod(filepath.Dir(socket), 0755); err != nil {
		t.Fatal(err)
	}
	reply, err := transport.RoundTrip(request)
	if reply != nil {
		_ = reply.Body.Close()
	}
	if err == nil {
		t.Fatal("permission drift did not fail closed")
	}
	if requests.Load() != 1 {
		t.Fatal("permission drift reached relay")
	}
}

func TestPilotTransportRejectsMissingPublicAndSymlinkSockets(t *testing.T) {
	socket := pilotTestSocket(t, http.NotFoundHandler())
	link := filepath.Join(filepath.Dir(socket), "link.sock")
	if err := os.Symlink(socket, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"relative.sock", link, socket + ".missing"} {
		if _, err := newPilotTransport(path); err == nil {
			t.Fatal("accepted unsafe relay path")
		}
	}
	if err := os.Chmod(socket, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := newPilotTransport(socket); err == nil {
		t.Fatal("accepted public socket")
	}
}

func TestPilotConfigurationIsIndependentAndDefaultOff(t *testing.T) {
	analytics, err := NewAnalytics(strings.Repeat("a", 32), "gregale_internal_pilot", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	domains, err := NewCloudflareDomains(strings.Repeat("a", 32), strings.Repeat("b", 32), "11111111-1111-4111-8111-111111111111", "test-token", "customers.hosting.example")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Analytics: analytics, Domains: domains, Routes: &KV{client: &http.Client{}}}
	values := map[string]string{}
	get := func(key string) string { return values[key] }
	if err := configurePilot(cfg, get); err != nil || analytics.client.Transport != nil || domains.client.Transport != nil || cfg.Routes.client.Transport != nil {
		t.Fatal("pilot affected default client")
	}
	values["FAAS_FRONTEND_HOSTING_PILOT_RELAY_SOCKET"] = "/tmp/does-not-exist.sock"
	if err := configurePilot(cfg, get); err == nil {
		t.Fatal("socket silently enabled pilot")
	}
	values["FAAS_FRONTEND_HOSTING_PILOT_ENABLED"] = "true"
	if err := configurePilot(cfg, get); err == nil {
		t.Fatal("missing pilot socket accepted")
	}
	socket := pilotTestSocket(t, http.NotFoundHandler())
	values["FAAS_FRONTEND_HOSTING_PILOT_RELAY_SOCKET"] = socket
	if err := configurePilot(cfg, get); err != nil {
		t.Fatal(err)
	}
	if domains.client.Transport != analytics.client.Transport || cfg.Routes.client.Transport != nil {
		t.Fatal("pilot changed unrelated clients or missed domain transport")
	}
	if _, ok := analytics.client.Transport.(*pilotTransport); !ok {
		t.Fatal("pilot was not installed on analytics client")
	}
	if _, err := Load(get, "apps.gregale.dev"); err == nil {
		t.Fatal("enabled pilot accepted without isolated hosting configuration")
	}
}
