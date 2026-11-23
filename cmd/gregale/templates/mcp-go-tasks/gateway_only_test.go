package main

// adr: 20261122194324 — gateway-only servers fail closed without the
// gateway's authorization mark.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayOnlyRequiresGatewayAuthorization(t *testing.T) {
	config, err := loadMCPConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.Auth = json.RawMessage(`{"mode":"api-key","resource":"https://tools.example/mcp","scopes":["tools"]}`)
	auth, err := newAuthenticator(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, mark := range []string{"", "rule-1"} {
		req := httptest.NewRequest(http.MethodPost, config.Endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		if mark != "" {
			req.Header.Set(gatewayAuthorizedHeader, mark)
		}
		w := httptest.NewRecorder()
		handleHTTP(config, auth, nil, w, req)
		if (w.Code == http.StatusUnauthorized) != (mark == "") {
			t.Fatalf("mark %q: status %d body %s", mark, w.Code, w.Body.String())
		}
	}
	health := httptest.NewRecorder()
	handleHTTP(config, auth, nil, health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code == http.StatusUnauthorized {
		t.Fatal("health checks must not need the gateway mark")
	}
}
