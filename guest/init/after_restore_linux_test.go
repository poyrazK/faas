//go:build linux

package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCallAfterRestoreHook(t *testing.T) {
	var method, path, lifecycle, remote string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, lifecycle, remote = r.Method, r.URL.Path, r.Header.Get("X-Faas-After-Restore"), r.RemoteAddr
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	port := testRestoreHookPort(t, server.URL)
	if err := callAfterRestoreHook(api.AfterRestoreHook{Path: "/internal/restore"}, port); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if method != http.MethodPost || path != "/internal/restore" || lifecycle != "1" {
		t.Fatalf("request = %s %s lifecycle=%q", method, path, lifecycle)
	}
	host, _, err := net.SplitHostPort(remote)
	if err != nil || host != "127.0.0.1" {
		t.Fatalf("remote address = %q, err=%v", remote, err)
	}
}

func TestCallAfterRestoreHookFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		wait    time.Duration
		timeout int
		want    string
	}{
		{name: "non-2xx", status: http.StatusServiceUnavailable, want: "HTTP 503"},
		{name: "redirect", status: http.StatusFound, want: "HTTP 302"},
		{name: "timeout", status: http.StatusNoContent, wait: 50 * time.Millisecond, timeout: 5, want: "deadline exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(tc.wait)
				if tc.status == http.StatusFound {
					w.Header().Set("Location", "http://example.com/")
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			err := callAfterRestoreHook(api.AfterRestoreHook{Path: "/restore", TimeoutMS: tc.timeout}, testRestoreHookPort(t, server.URL))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func testRestoreHookPort(t *testing.T, raw string) int {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}
