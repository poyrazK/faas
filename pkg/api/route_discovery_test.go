package api

import "testing"

// TestRouteDiscoverableStatus: a client error does not prove a route exists
// (Hunt 12 H12-17).
func TestRouteDiscoverableStatus(t *testing.T) {
	for status, want := range map[int]bool{200: true, 204: true, 301: true, 304: true, 500: true, 503: true, 400: false, 401: false, 403: false, 404: false, 405: false, 429: false} {
		if got := RouteDiscoverableStatus(status); got != want {
			t.Errorf("RouteDiscoverableStatus(%d) = %v, want %v", status, got, want)
		}
	}
}
