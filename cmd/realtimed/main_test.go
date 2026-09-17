package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/oci"
)

func TestNewCallbackHTTPClientUsesGuardedTransport(t *testing.T) {
	t.Setenv("FAAS_EGRESS_ALLOW_LOOPBACK", "")

	wantTimeout := 7 * time.Second
	client := newCallbackHTTPClient(wantTimeout)
	if client.Timeout != wantTimeout {
		t.Fatalf("client timeout = %s, want %s", client.Timeout, wantTimeout)
	}
	if client.CheckRedirect == nil {
		t.Fatal("callback client must reject redirects")
	}
	if err := client.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect error = %v, want http.ErrUseLastResponse", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.DialContext == nil {
		t.Fatalf("callback client transport = %T, want an HTTP transport with an egress dialer", client.Transport)
	}
}

func TestNewCallbackHTTPClientRejectsLoopbackByDefault(t *testing.T) {
	t.Setenv("FAAS_EGRESS_ALLOW_LOOPBACK", "")

	var reached atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached.Store(true)
	}))
	defer server.Close()

	client := newCallbackHTTPClient(time.Second)
	resp, err := client.Get(server.URL)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("loopback callback unexpectedly succeeded")
	}
	if !errors.Is(err, oci.ErrImageEgressDenied) && !errors.Is(err, oci.ErrEgressDenied) {
		t.Fatalf("loopback callback error = %v, want an egress-denial error", err)
	}
	if reached.Load() {
		t.Fatal("loopback callback unexpectedly reached the server")
	}
}

func TestNewCallbackHTTPClientAllowsLoopbackOnlyWithExplicitOptIn(t *testing.T) {
	t.Setenv("FAAS_EGRESS_ALLOW_LOOPBACK", "1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := newCallbackHTTPClient(time.Second)
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("loopback callback with explicit opt-in: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("callback status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
}
