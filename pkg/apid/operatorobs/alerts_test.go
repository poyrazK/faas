package operatorobs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/promql"
)

func TestFiringAlertsEnrichmentPreservesIdentityAndClock(t *testing.T) {
	labels := map[string]string{"alertname": "FaasApiDown", "alertstate": "firing", "severity": "page", "component": "apid", "app_id": "11111111-1111-4111-8111-111111111111"}
	var metadataDown atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/alerts" {
			if metadataDown.Load() {
				http.Error(w, "offline", 503)
				return
			}
			metadataLabels := map[string]string{}
			for key, value := range labels {
				if key != "alertstate" {
					metadataLabels[key] = value
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"alerts": []promql.Alert{{Labels: metadataLabels, State: "firing", Annotations: map[string]string{
				"runbook_url": "https://github.com/poyrazK/faas/blob/main/docs/runbooks/FaasApiDown.md", "description": "private-payload",
			}}}}})
			return
		}
		if !strings.Contains(r.URL.Query().Get("query"), "ALERTS_FOR_STATE") {
			t.Error("lost persisted activation query")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": labels, "value": []any{1728000000, "1727999900"}}}}})
	}))
	defer srv.Close()
	client := promql.NewClient(srv.URL, srv.Client())
	items, missing := FiringAlerts(context.Background(), client)
	if len(items) != 1 || len(missing) != 0 {
		t.Fatalf("items=%+v missing=%v", items, missing)
	}
	first := items[0]
	if first.Severity != "critical" || first.ObservedAt.Unix() != 1727999900 || !strings.HasSuffix(first.RunbookURL, "FaasApiDown.md") || !strings.HasSuffix(first.ActionPath, labels["app_id"]) || first.EvidencePath == "" {
		t.Fatalf("enrichment missing: %+v", first)
	}
	id := strings.TrimPrefix(first.ID, "platform_alert/")
	evidence, err := Evidence(context.Background(), nil, client, "platform_alert", id, 25, time.Now())
	if err != nil || evidence.Incident.ID != first.ID || evidence.History == nil {
		t.Fatalf("alert evidence=%+v err=%v", evidence, err)
	}
	metadataDown.Store(true)
	items, missing = FiringAlerts(context.Background(), client)
	if len(items) != 1 || items[0].ID != first.ID || !items[0].ObservedAt.Equal(first.ObservedAt) || fmt.Sprint(missing) != "[prometheus_alert_metadata]" {
		t.Fatalf("metadata failure hid signal or changed identity: %+v, %v", items, missing)
	}
	evidence, err = Evidence(context.Background(), nil, client, "platform_alert", id, 25, time.Now())
	if err != nil || len(evidence.UnavailableSources) != 1 {
		t.Fatalf("annotation failure hid alert evidence: %+v, %v", evidence, err)
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), "private-payload") {
		t.Fatal("raw annotation exposed")
	}
}

func TestRunbookAndResourceLabelsAreProjectedSafely(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "//evil.example/docs/runbook", "https://evil.example/runbook", "/docs/../secret", "/docs/%2e%2e/secret", "https://user:secret@github.com/poyrazK/faas/blob/main/docs/r.md", "/docs/r.md?token=secret", "/docs/r.md\nforged"} {
		if got := safeRunbookURL(raw); got != "" {
			t.Errorf("unsafe link %q accepted as %q", raw, got)
		}
	}
	for _, raw := range []string{"/docs/runbooks/FaasApiDown.md", "https://github.com/poyrazK/faas/blob/main/docs/runbooks/FaasApiDown.md#triage"} {
		if safeRunbookURL(raw) != raw {
			t.Errorf("valid runbook rejected: %q", raw)
		}
	}
	item := projectAlert(promql.VectorSample{Labels: map[string]string{"alertname": "FaasApiDown", "account_id": "../../secret", "app_id": "x?token=secret"}, Value: 1727999900})
	if item.AccountID != "" || item.AppID != "" || item.ActionPath != "/v1/admin/obs/health" {
		t.Fatalf("invalid resource identifiers exposed: %+v", item)
	}
	const nodeID = "11111111-1111-4111-8111-111111111111"
	item = projectAlert(promql.VectorSample{Labels: map[string]string{"alertname": "FaasHighCPU", "node": nodeID}, Value: 1727999900})
	if item.NodeID != nodeID || item.ActionPath != EvidencePath("compute_node", nodeID) {
		t.Fatalf("existing metric label did not link to node evidence: %+v", item)
	}
}
