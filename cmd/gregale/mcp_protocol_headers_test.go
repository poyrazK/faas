package main_test

import (
	"net/http"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Production-us rc.294: every `gregale mcp deploy` of a Gregale starter failed
// qualification. The starters answer notifications/initialized with a bare
// 202 Accepted, which has no protocol version to echo, and the qualification
// transports demanded MCP-Protocol-Version on every 2xx (Hunt 14 H14-2).
func TestMCPProtocolResponseHeadersAcceptABareNotificationAck(t *testing.T) {
	version := api.MCPProtocolVersion
	for _, tc := range []struct {
		name    string
		status  int
		headers map[string]string
		wantErr bool
	}{
		{name: "bare 202 notification ack", status: http.StatusAccepted},
		{name: "202 that opens a session", status: http.StatusAccepted, headers: map[string]string{"MCP-Session-Id": "s-1"}, wantErr: true},
		{name: "200 naming the version", status: http.StatusOK, headers: map[string]string{"MCP-Protocol-Version": version}},
		{name: "200 without the version", status: http.StatusOK, wantErr: true},
		{name: "200 with another version", status: http.StatusOK, headers: map[string]string{"MCP-Protocol-Version": api.MCPLegacyProtocolVersion}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range tc.headers {
				h.Set(k, v)
			}
			if err := main.ValidateMCPProtocolResponseHeaders(tc.status, h, version); (err != nil) != tc.wantErr {
				t.Fatalf("validate(%d, %v) error = %v, want error %v", tc.status, tc.headers, err, tc.wantErr)
			}
		})
	}
}
