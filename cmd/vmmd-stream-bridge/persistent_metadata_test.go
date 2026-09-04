package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPersistentRequestUsesWireMetadata(t *testing.T) {
	guestLn, guest := startFakeGuest(t)
	defer func() { _ = guestLn.Close() }()
	_, portText, err := net.SplitHostPort(guestLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAAS_BRIDGE_PROTOCOL", "h1")
	t.Setenv("FAAS_BRIDGE_METHOD", "GET")
	t.Setenv("FAAS_BRIDGE_URL", "/stale-env")
	t.Setenv("FAAS_BRIDGE_HOST", "stale.example")
	t.Setenv("FAAS_BRIDGE_HEADERS", "X-Stale=must-not-win")

	req := httptest.NewRequest(http.MethodPost, "http://unix/from-wire?ok=1", strings.NewReader("payload"))
	req.Header.Set(bridgeRequestMarkerHeader, "1")
	req.Header.Set(bridgeRequestProtocolHeader, "h1")
	req.Header.Set(bridgeRequestPortHeader, strconv.FormatUint(port, 10))
	req.Header.Set(bridgeRequestHostHeader, "wire.example")
	req.Header.Set("X-Wire", "present")
	rr := httptest.NewRecorder()
	newHandler("127.0.0.1", uint16(port), time.Now().Add(5*time.Second)).ServeHTTP(rr, req)
	_, _ = io.Copy(io.Discard, rr.Result().Body)
	_ = rr.Result().Body.Close()

	guest.mu.Lock()
	got := string(guest.request)
	guest.mu.Unlock()
	if !strings.HasPrefix(got, "POST /from-wire?ok=1 HTTP/1.1\r\n") {
		t.Fatalf("request line = %q, want wire request metadata", firstLine(got))
	}
	if !strings.Contains(got, "Host: wire.example\r\n") {
		t.Fatalf("wire host missing: %q", got)
	}
	if !strings.Contains(got, "X-Wire: present\r\n") {
		t.Fatalf("wire header missing: %q", got)
	}
	if strings.Contains(got, "stale-env") || strings.Contains(got, "stale.example") || strings.Contains(got, "X-Stale") {
		t.Fatalf("stale environment metadata won over request metadata: %q", got)
	}
	for _, private := range []string{bridgeRequestMarkerHeader, bridgeRequestProtocolHeader, bridgeRequestPortHeader, bridgeRequestHostHeader} {
		if strings.Contains(got, private) {
			t.Fatalf("private bridge header leaked to guest: %s in %q", private, got)
		}
	}
}
