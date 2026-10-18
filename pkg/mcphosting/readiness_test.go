package mcphosting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadinessReadOnlyAndFailures(t *testing.T) {
	for _, mode := range []string{"healthy", "ping_failure", "bad_version", "session", "bad_negotiation", "unauthorized", "incomplete_catalog", "catalog_drift", "redirect", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			calls := map[string]int{}
			redirected := 0
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected++ }))
			defer destination.Close()
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				calls[req.Method]++
				if r.Header.Get("Authorization") != "Bearer PROBE_SECRET" {
					t.Error("probe token missing")
				}
				if mode == "unauthorized" {
					w.WriteHeader(401)
					_, _ = w.Write([]byte("PRIVATE_SERVER_BODY"))
					return
				}
				if mode == "redirect" {
					http.Redirect(w, r, destination.URL, 307)
					return
				}
				if mode == "deadline" {
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("MCP-Protocol-Version", ProtocolVersion)
				if mode == "bad_version" {
					w.Header().Set("MCP-Protocol-Version", LegacyProtocolVersion)
				}
				if mode == "session" {
					w.Header().Set("MCP-Session-Id", "PRIVATE_SESSION")
				}
				result := `{}`
				switch req.Method {
				case "initialize":
					version := ProtocolVersion
					if mode == "bad_negotiation" {
						version = LegacyProtocolVersion
					}
					result = fmt.Sprintf(`{"protocolVersion":%q,"capabilities":{"tools":{}}}`, version)
				case "notifications/initialized":
					w.WriteHeader(202)
					return
				case "ping":
					if mode == "ping_failure" {
						w.WriteHeader(503)
						return
					}
				case "tools/list":
					result = `{"tools":[{"name":"add","inputSchema":{"type":"object"}}]}`
					if mode == "incomplete_catalog" {
						result = `{"tools":[{"name":"private-name","inputSchema":null}]}`
					}
				default:
					t.Errorf("unexpected or side-effecting method %q", req.Method)
					w.WriteHeader(400)
					return
				}
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
			})
			c.Token = "PROBE_SECRET"
			baseline, _ := NewContract(ProtocolVersion, []Tool{{Name: "add", InputSchema: map[string]any{"type": "object"}}})
			if mode == "catalog_drift" {
				baseline.Tools = nil
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if mode == "deadline" {
				cancel()
			}
			report := CheckReadiness(ctx, c, &baseline)
			if report.OK != (mode == "healthy") {
				t.Fatalf("unexpected readiness: %+v", report)
			}
			if mode == "healthy" && (calls["ping"] != 1 || calls["tools/list"] != 1 || len(report.CatalogSHA256) != 64) {
				t.Fatalf("missing probes: %+v %+v", calls, report)
			}
			data, _ := json.Marshal(report)
			for _, secret := range []string{"PROBE_SECRET", "PRIVATE_SERVER_BODY", "PRIVATE_SESSION", "private-name"} {
				if strings.Contains(string(data), secret) {
					t.Fatalf("private data escaped report: %s", data)
				}
			}
			if redirected != 0 {
				t.Fatal("followed redirect")
			}
		})
	}
}

func TestReadinessInvalidBaselineDoesNotProbe(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { calls++ })
	invalid := Contract{Version: 99}
	r := CheckReadiness(t.Context(), c, &invalid)
	if r.OK || calls != 0 {
		t.Fatal("invalid baseline reached endpoint")
	}
}

func TestReadinessMetadataOnlyServer(t *testing.T) {
	calls := map[string]int{}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls[req.Method]++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("MCP-Protocol-Version", ProtocolVersion)
		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{"resources": map[string]any{}, "prompts": map[string]any{}}}
		case "notifications/initialized":
			w.WriteHeader(202)
			return
		case "ping":
		case "server/discover":
			result = map[string]any{"supportedVersions": []string{ProtocolVersion}, "capabilities": map[string]any{"resources": map[string]any{}, "prompts": map[string]any{}}}
		case "resources/list":
			result = map[string]any{"resources": []any{}}
		case "resources/templates/list":
			result = map[string]any{"resourceTemplates": []any{}}
		case "prompts/list":
			result = map[string]any{"prompts": []any{}}
		default:
			t.Errorf("unexpected or side-effecting method: %s", req.Method)
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	})
	report := CheckReadiness(t.Context(), c, nil)
	if !report.OK || calls["tools/list"] != 0 || calls["resources/list"] != 1 || calls["prompts/list"] != 1 {
		t.Fatalf("metadata-only readiness: %+v calls=%+v", report, calls)
	}
}
