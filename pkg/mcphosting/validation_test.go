package mcphosting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestValidateCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session bool
		schema  string
		version string
		want    bool
	}{
		{name: "valid", schema: `{"type":"object"}`, version: ProtocolVersion, want: true},
		{name: "stateful", session: true, schema: `{"type":"object"}`, version: ProtocolVersion},
		{name: "invalid schema", schema: `{"type":"string"}`, version: ProtocolVersion},
		{name: "wrong negotiation", schema: `{"type":"object"}`, version: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			methods := []string{}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				methods = append(methods, req.Method)
				w.Header().Set("Content-Type", "application/json")
				if tc.session {
					w.Header().Set("MCP-Session-Id", "private-secret-session")
				}
				var result string
				switch req.Method {
				case "initialize":
					result = fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{"tools":{}}}`, tc.version)
				case "notifications/initialized":
					w.WriteHeader(202)
					return
				case "server/discover":
					result = fmt.Sprintf(`{"supportedVersions":[%q],"capabilities":{"tools":{}}}`, ProtocolVersion)
				case "tools/list":
					result = `{"tools":[{"name":"hello","inputSchema":` + tc.schema + `}]}`
				default:
					t.Errorf("unexpected operation %s", req.Method)
					w.WriteHeader(500)
					return
				}
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
			})
			report := ValidateCompatibility(context.Background(), c, nil)
			if report.OK != tc.want {
				t.Fatalf("%+v", report)
			}
			encoded, _ := json.Marshal(report)
			if strings.Contains(string(encoded), "private-secret-session") {
				t.Fatal("secret leaked")
			}
			if tc.want && (report.Tools != 1 || len(report.CatalogSHA256) != 64) {
				t.Fatalf("missing catalog summary: %+v", report)
			}
			if !tc.want && len(methods) > 4 {
				t.Fatal("validation did not stop on critical failure")
			}
		})
	}
}

func TestValidationSanitizesRemoteFailure(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"credential=private-secret"}}`)
	})
	report := ValidateCompatibility(context.Background(), c, nil)
	encoded, _ := json.Marshal(report)
	if report.OK || strings.Contains(string(encoded), "private-secret") {
		t.Fatalf("unsafe report: %s", encoded)
	}
}
