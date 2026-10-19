package outboundai

// adr: 1438 — a day range of usage by tag exports as NDJSON or CSV,
// optionally for one key, and budget alerts name their tag.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTagUsageExport(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	today := time.Now().UTC()
	yesterday := today.AddDate(0, 0, -1).Format("2006-01-02")
	store.SeedOutboundAITagUsage(integrationID, today.Format("2006-01-02"),
		state.OutboundAITagUsageItem{Tag: "tier=free", Requests: 1, InputTokens: 3, OutputTokens: 1, CostMillicents: 5},
		state.OutboundAITagUsageItem{Tag: "feature=search", Requests: 2, InputTokens: 10, OutputTokens: 4, CostMillicents: 40})
	store.SeedOutboundAITagUsage(integrationID, yesterday,
		state.OutboundAITagUsageItem{Tag: "feature=chat", Requests: 7, InputTokens: 70, OutputTokens: 7, CostMillicents: 700})
	path := "/v1/outbound/integrations/x/ai-usage/tags/export?from=" + yesterday
	w := serve(service.ExportTagUsage, acct, http.MethodGet, path, integrationID, "")
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	var first api.OutboundAITagUsageExportRow
	if w.Code != http.StatusOK || len(lines) != 3 || json.Unmarshal([]byte(lines[0]), &first) != nil || first.Tag != "feature=chat" ||
		first.UsageDate != yesterday || first.Tokens != 77 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/x-ndjson") {
		t.Fatalf("NDJSON export = %d %v %q", w.Code, w.Header(), w.Body.String())
	}
	w = serve(service.ExportTagUsage, acct, http.MethodGet, path+"&format=csv&key=feature", integrationID, "")
	csvLines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if w.Code != http.StatusOK || len(csvLines) != 3 || csvLines[0] != strings.Join(api.OutboundAITagUsageExportColumns, ",") ||
		!strings.Contains(csvLines[2], ",feature=search,2,0,10,4,14,40") || strings.Contains(w.Body.String(), "tier=free") {
		t.Fatalf("CSV export = %d %q", w.Code, w.Body.String())
	}
	for _, query := range []string{"?format=xml", "?from=2001-01-01", "?key=Feature", "?from=" + today.Format("2006-01-02") + "&to=" + yesterday} {
		if w := serve(service.ExportTagUsage, acct, http.MethodGet, "/v1/outbound/integrations/x/ai-usage/tags/export"+query, integrationID, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", query, w.Code)
		}
	}
	other, err := store.CreateAccount(context.Background(), "other-tags@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if w := serve(service.ExportTagUsage, other, http.MethodGet, path, integrationID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("another account's export = %d", w.Code)
	}
}
