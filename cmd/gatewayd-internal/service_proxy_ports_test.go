// adr: 384
package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestGuestServiceHTTPPortsShareHandlerAndTransport(t *testing.T) {
	deps := defaultDeps()
	addresses := map[string]string{}
	deps.listen = func(network, requested string) (net.Listener, error) {
		listener, err := net.Listen(network, "127.0.0.1:0")
		if err == nil {
			addresses[requested] = listener.Addr().String()
		}
		return listener, err
	}
	var servers []*http.Server
	add := func(server *http.Server) {
		servers = append(servers, server)
		t.Cleanup(func() { _ = server.Close() })
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Identity") != "allowed" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = fmt.Fprint(w, r.Host+" "+r.URL.Path)
	})
	if err := startGuestServiceHTTPListeners(deps, "172.19.0.1:10080", handler, add, make(chan error, 2), discardLogger()); err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("started %d servers, want two compatibility listeners", len(servers))
	}
	for _, server := range servers {
		if !server.Protocols.HTTP1() || !server.Protocols.UnencryptedHTTP2() || server.WriteTimeout != writeTimeoutOrDefault(0) {
			t.Fatalf("listener %s lost H1/H2C or request envelope", server.Addr)
		}
		for _, authorized := range []bool{false, true} {
			request, err := http.NewRequest(http.MethodGet, "http://"+addresses[server.Addr]+"/orders", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Host = "orders.svc.gregale"
			if authorized {
				request.Header.Set("X-Test-Identity", "allowed")
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			want := http.StatusForbidden
			if authorized {
				want = http.StatusOK
				if string(body) != "orders.svc.gregale /orders" {
					t.Fatalf("listener rewrote request: %s", body)
				}
			}
			if response.StatusCode != want {
				t.Fatalf("%s authorized=%v: status=%d, want %d", server.Addr, authorized, response.StatusCode, want)
			}
		}
	}
	if _, ok := addresses["172.19.0.1:10081"]; !ok {
		t.Fatal("canonical listener absent")
	}
	if _, ok := addresses["172.19.0.1:10080"]; !ok {
		t.Fatal("legacy listener absent")
	}
}

func TestGuestServiceHTTPPortBindFailureRejectsStartup(t *testing.T) {
	deps := defaultDeps()
	deps.listen = func(_, addr string) (net.Listener, error) { return nil, errors.New("port occupied") }
	err := startGuestServiceHTTPListeners(deps, "10.100.0.1:10080", http.NotFoundHandler(), func(*http.Server) {}, make(chan error, 2), discardLogger())
	if err == nil || !strings.Contains(err.Error(), "10081") || !strings.Contains(err.Error(), "port occupied") {
		t.Fatalf("bind failure = %v; want canonical listener startup failure", err)
	}
}
