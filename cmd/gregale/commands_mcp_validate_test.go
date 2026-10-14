package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPValidateTransportGate(t *testing.T) {
	for _, tc := range []struct {
		name, header string
		want         bool
	}{
		{"valid", mcphosting.ProtocolVersion, true}, {"missing header", "", false}, {"wrong header", "unsupported", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("MCP-Protocol-Version", tc.header)
				result := ""
				switch req.Method {
				case "initialize":
					result = fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{"tools":{}}}`, mcphosting.ProtocolVersion)
				case "notifications/initialized":
					w.WriteHeader(202)
					return
				case "server/discover":
					result = fmt.Sprintf(`{"supportedVersions":[%q],"capabilities":{"tools":{}}}`, mcphosting.ProtocolVersion)
				case "tools/list":
					result = `{"tools":[]}`
				default:
					t.Errorf("unexpected method: %s", req.Method)
					w.WriteHeader(500)
					return
				}
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
			}))
			defer server.Close()
			report, err := validateMCPEndpoint(context.Background(), server.URL+"/mcp", "", mcphosting.ProtocolVersion, time.Second)
			if err != nil || report.OK != tc.want {
				t.Fatalf("report=%+v err=%v", report, err)
			}
		})
	}
}

func TestMCPValidateDeployStopsBeforeUpload(t *testing.T) {
	oldOut, oldErr := osStdout, osStderr
	var output, diagnostics bytes.Buffer
	osStdout, osStderr = &output, &diagnostics
	defer func() { osStdout, osStderr = oldOut, oldErr }()
	dir := t.TempDir()
	config := `{"version":1,"endpoint":"/mcp","transport":"streamable-http","mode":"stateless","allowed_origins":[],"auth":{"mode":"open"}}`
	if err := os.WriteFile(filepath.Join(dir, mcphosting.ConfigFile), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(503) }))
	defer server.Close()
	code := runMCPDeploy(mcpDeployOptions{path: dir, slug: "validation-gated", validateEndpoint: server.URL + "/mcp", timeout: 60})
	if code == 0 || requests != 1 || !strings.Contains(diagnostics.String(), "validation failed before source upload") {
		t.Fatalf("code=%d requests=%d stderr=%s", code, requests, diagnostics.String())
	}
}
