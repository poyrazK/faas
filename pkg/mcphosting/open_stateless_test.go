package mcphosting

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// strictModernServer answers like the official mcp-node starter on revision
// 2026-07-28: there is no initialize, so it refuses one with -32020, and a
// client opens with server/discover. It records every method it received.
func strictModernServer(t *testing.T) (*httptest.Server, func() map[string]int) {
	t.Helper()
	var mu sync.Mutex
	calls := map[string]int{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		calls[req.Method]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("MCP-Protocol-Version", ProtocolVersion)
		var result string
		switch req.Method {
		case "server/discover":
			result = fmt.Sprintf(`{"supportedVersions":[%q],"capabilities":{"tools":{}}}`, ProtocolVersion)
		case "ping":
			result = `{}`
		case "tools/list":
			result = `{"tools":[{"name":"add","inputSchema":{"type":"object"}}]}`
		case "initialize", "notifications/initialized":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32020,"message":"initialize is not part of this protocol"}}`, req.ID)
			return
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32601}}`, req.ID)
			return
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
	}))
	t.Cleanup(s.Close)
	return s, func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := make(map[string]int, len(calls))
		for k, v := range calls {
			out[k] = v
		}
		return out
	}
}

// Prod CLI e2e 2026-11-14: hosting qualification sent initialize on revision
// 2026-07-28, which the official starter refuses, so a correct server never
// passed readiness and never took traffic. Every qualification path must open
// with server/discover on that revision and never send initialize.
func TestQualificationOpensModernServersWithDiscover(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(t *testing.T, endpoint string) error
	}{
		{name: "validate compatibility", run: func(t *testing.T, endpoint string) error {
			c, err := NewClient(endpoint, "", ProtocolVersion)
			if err != nil {
				return err
			}
			if report := ValidateCompatibility(t.Context(), c, nil); !report.OK {
				return fmt.Errorf("validation failed: %+v", report.Checks)
			}
			return nil
		}},
		{name: "readiness", run: func(t *testing.T, endpoint string) error {
			c, err := NewClient(endpoint, "", ProtocolVersion)
			if err != nil {
				return err
			}
			if report := CheckReadiness(t.Context(), c, nil); !report.OK {
				return fmt.Errorf("readiness failed: %+v", report.Checks)
			}
			return nil
		}},
		{name: "catalog observer", run: func(t *testing.T, endpoint string) error {
			_, err := ObserveCatalog(t.Context(), endpoint, "", ProtocolVersion, nil)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, calls := strictModernServer(t)
			if err := tc.run(t, s.URL+"/mcp"); err != nil {
				t.Fatal(err)
			}
			got := calls()
			if got["initialize"] != 0 || got["notifications/initialized"] != 0 {
				t.Fatalf("sent the legacy handshake on %s: %v", ProtocolVersion, got)
			}
			if got["server/discover"] == 0 {
				t.Fatalf("never opened with server/discover: %v", got)
			}
		})
	}
}

// The legacy protocol still opens with initialize.
func TestOpenStatelessKeepsInitializeForLegacy(t *testing.T) {
	var methods []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		methods = append(methods, req.Method)
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":%q,"capabilities":{"tools":{}}}}`, req.ID, LegacyProtocolVersion)
	}))
	t.Cleanup(s.Close)
	c, err := NewClient(s.URL+"/mcp", "", LegacyProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.OpenStateless(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 || methods[0] != "initialize" || methods[1] != "notifications/initialized" {
		t.Fatalf("legacy opening = %v, want initialize then notifications/initialized", methods)
	}
}
