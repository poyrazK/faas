package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadinessDialerUsesConfiguredTLS(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	tlsConfig := server.Client().Transport.(*http.Transport).TLSClientConfig
	target := "tcp://" + strings.TrimPrefix(server.URL, "https://")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := tlsReadinessDialer(tlsConfig)(ctx, target); err != nil {
		t.Fatalf("trusted TLS dial: %v", err)
	}
	if err := tlsReadinessDialer(nil)(ctx, target); err == nil {
		t.Fatal("TCP readiness accepted missing TLS configuration")
	}
}
