package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowserCORSWireContract(t *testing.T) {
	config, err := loadMCPConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.AllowedOrigins = []string{"https://trusted.example"}
	for _, tc := range []struct {
		name, origin, method, headers, path string
		status                              int
	}{
		{"private preflight", "https://trusted.example", "POST", "Authorization, Content-Type, MCP-Protocol-Version, MCP-Method, MCP-Name, MCP-Param-report", config.Endpoint, 204},
		{"untrusted", "https://evil.example", "POST", "Authorization", config.Endpoint, 403},
		{"missing origin", "", "POST", "Authorization", config.Endpoint, 405},
		{"unsupported header", "https://trusted.example", "POST", "X-Secret", config.Endpoint, 400},
		{"invalid param", "https://trusted.example", "POST", "mcp-param-", config.Endpoint, 400},
		{"unsupported method", "https://trusted.example", "DELETE", "Authorization", config.Endpoint, 400},
		{"missing method", "https://trusted.example", "", "Authorization", config.Endpoint, 400},
		{"unknown route", "https://trusted.example", "POST", "Authorization", "/missing", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, tc.path, nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.method != "" {
				req.Header.Set("Access-Control-Request-Method", tc.method)
			}
			req.Header.Set("Access-Control-Request-Headers", tc.headers)
			w := httptest.NewRecorder()
			// Preflight must finish before catalog/authentication, even for private apps.
			handleHTTP(config, nil, nil, w, req)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.status == 204 {
				if w.Body.Len() != 0 || w.Header().Get("Access-Control-Allow-Origin") != tc.origin || w.Header().Get("Access-Control-Allow-Headers") == "" {
					t.Fatalf("invalid preflight: %v", w.Result())
				}
			} else if w.Header().Get("Access-Control-Allow-Methods") != "" {
				t.Fatal("invalid preflight granted methods")
			}
			if tc.status == 403 && w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("reflected untrusted origin")
			}
		})
	}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://trusted.example")
	w := httptest.NewRecorder()
	handleHTTP(config, nil, nil, w, req)
	if w.Header().Get("Access-Control-Expose-Headers") != "WWW-Authenticate, MCP-Protocol-Version, Streaming-Status" {
		t.Fatal("missing exposed protocol headers")
	}
	req.Header.Add("Origin", "https://trusted.example")
	w = httptest.NewRecorder()
	handleHTTP(config, nil, nil, w, req)
	if w.Code != 403 {
		t.Fatal("accepted duplicate Origin")
	}
}
