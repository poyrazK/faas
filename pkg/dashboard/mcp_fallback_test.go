package dashboard

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPOperationsFallbackDeliveryHistory(t *testing.T) {
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	data := AlertRuleDetailData{Rule: state.AlertRule{Name: "MCP readiness"}, Deliveries: []state.AlertDelivery{{FiredAt: at, Status: state.AlertDeliveryDelivered, LastStatusCode: 200, AttemptCount: 1, ReadinessEventID: "logical-event", ReadinessDestination: "fallback", ReadinessFailoverReason: "primary_failure_threshold", LastError: "<script>receiver-error</script>"}}}
	w := httptest.NewRecorder()
	if err := Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-nonce", Page{Title: "Readiness deliveries", Body: "alert_rule_detail", Account: &AccountView{ID: "owner"}, Data: data}); err != nil {
		t.Fatal(err)
	}
	body := w.Body.String()
	for _, want := range []string{"2026-10-17 12:00:00 UTC", "readiness notification · fallback", "primary_failure_threshold", "logical-event", "HTTP 200", "&lt;script&gt;receiver-error&lt;/script&gt;"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(body, "<script>receiver-error") {
		t.Fatal("unescaped receiver error")
	}
}

func TestMCPOperationsDestinationQueues(t *testing.T) {
	version := int64(4)
	data := MCPOperationsData{AppFilter: "demo", ReadinessScope: "staging", AlertCSRF: "csrf", ReadinessNotifications: []MCPReadinessNotificationView{{MCPReadinessNotificationQueue: api.MCPReadinessNotificationQueue{RuleID: "rule", Name: "Readiness", OutstandingCount: 2}, DestinationQueues: []MCPReadinessDestinationQueueView{
		{MCPReadinessDestinationQueue: api.MCPReadinessDestinationQueue{Destination: "primary", State: "delayed", CanRetry: true, CanAbandon: true, OutstandingCount: 1, Head: &api.MCPReadinessIncidentEvent{EventID: "old-primary-event", DeliveryVersion: version}}, RetryRequestID: "primary-retry", AbandonRequestID: "primary-abandon"},
		{MCPReadinessDestinationQueue: api.MCPReadinessDestinationQueue{Destination: "fallback", State: "ready", CanAbandon: true, OutstandingCount: 1, Head: &api.MCPReadinessIncidentEvent{EventID: "new-fallback-event"}}, AbandonRequestID: "fallback-abandon"},
	}}}}
	w := httptest.NewRecorder()
	if err := Render(w, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-nonce", Page{Title: "MCP", Body: "mcp_operations", Account: &AccountView{ID: "owner"}, Data: data}); err != nil {
		t.Fatal(err)
	}
	body := w.Body.String()
	for _, want := range []string{`name="destination" value="primary"`, `name="destination" value="fallback"`, "old-primary-event", "new-fallback-event", "Retry primary now", "Abandon fallback delivery permanently"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q", want)
		}
	}
}
