package internal

import (
	"net/http/httptest"
	"testing"
)

func TestLifecycleContextRejectsForgedAndAmbiguousMarkers(t *testing.T) {
	for _, tc := range []struct {
		name, peer, method string
		before, after      []string
		want               string
	}{
		{"checkpoint", "127.0.0.1:1234", "POST", []string{"1"}, nil, "before_checkpoint"},
		{"restore IPv6", "[::1]:1234", "POST", nil, []string{"1"}, "after_restore"},
		{"mapped", "[::ffff:127.0.0.1]:1234", "POST", nil, []string{"1"}, "after_restore"},
		{"public spoof", "10.0.0.1:1234", "POST", []string{"1"}, nil, ""},
		{"other loopback", "127.0.0.2:1234", "POST", []string{"1"}, nil, ""},
		{"wrong method", "127.0.0.1:1234", "GET", []string{"1"}, nil, ""},
		{"unmarked", "127.0.0.1:1234", "POST", nil, nil, ""},
		{"duplicate", "127.0.0.1:1234", "POST", []string{"1", "1"}, nil, ""},
		{"combined", "127.0.0.1:1234", "POST", []string{"1, 1"}, nil, ""},
		{"both", "127.0.0.1:1234", "POST", []string{"1"}, []string{"1"}, ""},
		{"wrong value", "127.0.0.1:1234", "POST", []string{"true"}, nil, ""},
		{"malformed peer", "localhost:1234", "POST", []string{"1"}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/internal/hook", nil)
			r.RemoteAddr = tc.peer
			r.Header["X-Faas-Before-Checkpoint"] = tc.before
			r.Header["X-Faas-After-Restore"] = tc.after
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
			r.Header.Set("Forwarded", "for=127.0.0.1")
			if got := lifecycleHook(r); got != tc.want {
				t.Fatalf("context = %q, want %q", got, tc.want)
			}
		})
	}
}
