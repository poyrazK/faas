// adr: 20261026195952
package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestControlPlaneProxyRoutesHostedMCPBridgeOnAPIHostOnly(t *testing.T) {
	t.Setenv("FAAS_APPS_DOMAIN", "gregale.dev")
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != mcpFlowCookie+"=flow" {
			t.Errorf("bridge saw cookies %q", got)
		}
		http.SetCookie(w, &http.Cookie{Name: mcpFlowCookie, Value: "next", Path: "/", Secure: true})
		http.SetCookie(w, &http.Cookie{Name: "gregale_session", Value: "planted", Path: "/"})
		_, _ = io.WriteString(w, "bridge")
	}))
	t.Cleanup(bridge.Close)
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "apid")
	}))
	t.Cleanup(controlPlane.Close)
	compute := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "compute")
	})

	off, err := newControlPlaneProxy(controlPlane.URL, compute, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	off.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "https://api.gregale.dev/mcp", nil))
	if rec.Body.String() != "compute" {
		t.Fatalf("an unconfigured bridge path reached %q", rec.Body.String())
	}

	t.Setenv("FAAS_MCP_BRIDGE_LOOPBACK", bridge.URL)
	handler, err := newControlPlaneProxy(controlPlane.URL, compute, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host, path, want string
	}{
		{"api.gregale.dev", "/mcp", "bridge"},
		{"api.gregale.dev", "/mcp/oauth/token", "bridge"},
		{"api.gregale.dev", "/mcp/uploads/token", "bridge"},
		{"api.gregale.dev", "/.well-known/oauth-protected-resource/mcp", "bridge"},
		{"api.gregale.dev", "/.well-known/oauth-authorization-server/mcp", "bridge"},
		{"api.gregale.dev", "/.well-known/openid-configuration/mcp", "bridge"},
		{"api.gregale.dev", "/mcpx", "compute"},
		{"api.gregale.dev", "/oauth/token", "apid"},
		{"gregale.dev", "/mcp", "compute"},
		{"app.gregale.dev", "/mcp", "compute"},
		{"127.0.0.1", "/mcp", "compute"},
	} {
		t.Run(tc.host+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://"+tc.host+tc.path, strings.NewReader("{}"))
			req.AddCookie(&http.Cookie{Name: "gregale_session", Value: "secret"})
			req.AddCookie(&http.Cookie{Name: mcpFlowCookie, Value: "flow"})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if got := rec.Body.String(); got != tc.want {
				t.Fatalf("body = %q, want %q", got, tc.want)
			}
			if tc.want == "bridge" {
				if cookies := rec.Header().Values("Set-Cookie"); len(cookies) != 1 || !strings.HasPrefix(cookies[0], mcpFlowCookie+"=next") {
					t.Fatalf("Set-Cookie = %q", cookies)
				}
			}
		})
	}
	t.Setenv("FAAS_MCP_BRIDGE_LOOPBACK", "127.0.0.1:8787")
	if _, err := newControlPlaneProxy(controlPlane.URL, compute, slog.Default()); err == nil {
		t.Fatal("a target without a scheme was accepted")
	}
}
