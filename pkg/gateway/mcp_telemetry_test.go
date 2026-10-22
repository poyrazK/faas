// adr: 1257 — bounded MCP transport evidence and leak-free stream gauges.
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMCPTransportTelemetry(t *testing.T) {
	m := NewMetrics()
	h := &Handler{metrics: m}
	r := httptest.NewRequest("POST", "https://example.com/mcp", nil)
	r.Header.Set("MCP-Method", "tools/call")
	h.observeMCPRequest(r, 401, "app", time.Second)
	if got := testutil.ToFloat64(m.mcpRequests.WithLabelValues("app", "tools/call", "unauthorized")); got != 1 {
		t.Fatalf("got %v", got)
	}
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	h.observeMCPRequest(r.WithContext(ctx), 200, "app", time.Second)
	if got := testutil.ToFloat64(m.mcpRequests.WithLabelValues("app", "tools/call", "canceled")); got != 1 {
		t.Fatalf("got %v", got)
	}
	r.Header.Set("MCP-Method", strings.Repeat("private-payload", 100))
	h.observeMCPRequest(r, 500, "app", time.Second)
	if got := testutil.ToFloat64(m.mcpRequests.WithLabelValues("app", "other", "upstream_error")); got != 1 {
		t.Fatalf("got %v", got)
	}
	done := h.startMCPStream(r, App{ID: "app", Plan: api.PlanHobby}, true)
	if got := testutil.ToFloat64(m.mcpActive.WithLabelValues("app", "other")); got != 1 {
		t.Fatalf("got %v", got)
	}
	done()
	if got := testutil.ToFloat64(m.mcpActive.WithLabelValues("app", "other")); got != 0 {
		t.Fatalf("leaked stream %v", got)
	}
	families, err := m.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if strings.Contains(family.String(), "private-payload") {
			t.Fatal("unbounded label leaked")
		}
	}
}
func TestMCPStreamAbortPreservesPanicAndBalancesGauge(t *testing.T) {
	m := NewMetrics()
	h := &Handler{metrics: m}
	r := httptest.NewRequest("POST", "https://example.com/mcp", nil)
	r.Header.Set("MCP-Method", "tools/call")
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				err, ok := recovered.(error)
				if !ok || !errors.Is(err, http.ErrAbortHandler) {
					t.Fatalf("panic changed: %v", recovered)
				}
			} else {
				t.Fatal("forwarding abort was swallowed")
			}
		}()
		defer h.startMCPStream(r, App{ID: "app"}, true)()
		panic(http.ErrAbortHandler)
	}()
	if testutil.ToFloat64(m.mcpActive.WithLabelValues("app", "tools/call")) != 0 {
		t.Fatal("leaked stream")
	}
	if testutil.ToFloat64(m.mcpRequests.WithLabelValues("app", "tools/call", "interrupted_stream")) != 1 {
		t.Fatal("abort not recorded")
	}
}
func TestMCPPlainHTTPExcluded(t *testing.T) {
	m := NewMetrics()
	h := &Handler{metrics: m}
	r := httptest.NewRequest("GET", "https://example.com/", nil)
	h.observeMCPRequest(r, 500, "app", time.Second)
	h.startMCPStream(r, App{ID: "app"}, true)()
	if testutil.CollectAndCount(m.mcpRequests) != 0 || testutil.CollectAndCount(m.mcpActive) != 0 {
		t.Fatal("plain traffic included")
	}
}
