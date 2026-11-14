package granttest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// Runtime is a real TLS MCP transport for an open tool. Modes are normal (0),
// catalog drift (1), lost response (2), tool error (3), and wait for cancel (4).
type Runtime struct {
	Server      *httptest.Server
	Calls       atomic.Int32
	Mode        atomic.Int32
	Started     chan struct{}
	Credential  atomic.Value
	Reflections atomic.Value
}

func NewRuntime(t *testing.T, f *Fixture, credential ...string) *Runtime {
	t.Helper()
	a := &Runtime{Started: make(chan struct{})}
	token := ""
	if len(credential) > 0 {
		token = credential[0]
	}
	a.Credential.Store(token)
	a.Reflections.Store([]string{})
	a.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := ""
		if token := a.Credential.Load().(string); token != "" {
			expected = "Bearer " + token
		}
		if r.Header.Get("Authorization") != expected {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string                     `json:"name"`
				Arguments map[string]json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		result := func(value any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": value})
		}
		switch request.Method {
		case "server/discover":
			result(map[string]any{"supportedVersions": []string{mcphosting.ProtocolVersion}, "capabilities": map[string]any{"tools": map[string]any{}}})
		case "tools/list":
			c, err := mcphosting.ReadContract(strings.NewReader(string(f.PublicationA.Outcome.Catalog)))
			if err != nil {
				t.Error(err)
				return
			}
			if a.Mode.Load() == 1 {
				c.Tools[0].Description = "runtime drift"
			}
			result(map[string]any{"tools": c.Tools})
		case "tools/call":
			if request.Params.Name != "visible" {
				t.Error("worker called an ungranted sibling tool")
			}
			if a.Calls.Add(1) == 1 {
				close(a.Started)
			}
			switch a.Mode.Load() {
			case 2:
				connection, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = connection.Close()
				}
				return
			case 3:
				result(map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "known tool error"}}})
				return
			case 4:
				<-r.Context().Done()
				return
			}
			result(map[string]any{"content": []any{map[string]any{"type": "text", "text": f.AgentToken + " " + a.Credential.Load().(string) + " " + strings.Join(a.Reflections.Load().([]string), " ")}}, "structuredContent": request.Params.Arguments, "_meta": map[string]any{"private": "never-retain"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(a.Server.Close)
	f.PreviewURL = a.Server.URL
	return a
}
