package main

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPFromRequestUsesTrustedLocalProxyAddress(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/account", nil)
	req.RemoteAddr = "127.0.0.1:43210"
	req.Header.Set("X-Forwarded-For", "203.0.113.42")

	if got := clientIPFromRequest(req); got != "203.0.113.42" {
		t.Fatalf("clientIPFromRequest() = %q, want forwarded customer IP", got)
	}
}

func TestClientIPFromRequestRejectsUntrustedForwardedAddress(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/account", nil)
	req.RemoteAddr = "198.51.100.7:43210"
	req.Header.Set("X-Forwarded-For", "203.0.113.42")

	if got := clientIPFromRequest(req); got != "198.51.100.7" {
		t.Fatalf("clientIPFromRequest() = %q, want direct peer IP", got)
	}
}

func TestClientIPFromRequestRejectsForwardedChain(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/account", nil)
	req.RemoteAddr = "127.0.0.1:43210"
	req.Header.Set("X-Forwarded-For", "203.0.113.42, 198.51.100.7")

	if got := clientIPFromRequest(req); got != "127.0.0.1" {
		t.Fatalf("clientIPFromRequest() = %q, want trusted peer fallback", got)
	}
}
