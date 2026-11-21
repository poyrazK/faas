package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
)

// Prod 2026-11-16: /readyz probes that timed out while apid restarted were
// logged as ERROR "upstream unavailable". A request the client abandoned is
// not an upstream failure; one whose client is still waiting is.
func TestClientLeftControlPlaneRequest(t *testing.T) {
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name   string
		ctx    context.Context
		err    error
		isLeft bool
	}{
		{"client cancelled", gone, context.Canceled, true},
		{"client cancelled, wrapped", gone, errors.Join(errors.New("proxy"), context.Canceled), true},
		{"client waiting, upstream refused", context.Background(), errors.New("connection refused"), false},
		{"client waiting, upstream context cancelled", context.Background(), context.Canceled, false},
		{"client cancelled, upstream refused", gone, errors.New("connection refused"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/readyz", nil).WithContext(tt.ctx)
			if got := clientLeftControlPlaneRequest(r, tt.err); got != tt.isLeft {
				t.Fatalf("clientLeftControlPlaneRequest = %v, want %v", got, tt.isLeft)
			}
		})
	}
}
