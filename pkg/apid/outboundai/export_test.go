package outboundai

// adr: 995 — the per-end-user export streams a day range as NDJSON or CSV,
// and listed alerts name the end user of a per-user budget.

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestExportUserUsage(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	today := time.Now().UTC()
	yesterday := today.AddDate(0, 0, -1).Format("2006-01-02")
	store.SeedOutboundAIUserUsage(integrationID, yesterday,
		state.OutboundAIUserUsageItem{EndUser: "zed", Requests: 1, InputTokens: 3, OutputTokens: 2, CostMillicents: 1})
	store.SeedOutboundAIUserUsage(integrationID, today.Format("2006-01-02"),
		state.OutboundAIUserUsageItem{EndUser: "amy", Requests: 2, ErrorRequests: 1, InputTokens: 10, OutputTokens: 5, CostMillicents: 7})
	export := func(query string) (int, string, string) {
		w := serve(service.ExportUserUsage, acct, http.MethodGet, "/v1/outbound/integrations/x/ai-usage/users/export"+query, integrationID, "")
		return w.Code, w.Header().Get("Content-Type"), w.Body.String()
	}
	code, contentType, body := export("?from=" + yesterday + "&format=csv")
	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if code != http.StatusOK || !strings.HasPrefix(contentType, "text/csv") || err != nil || len(records) != 3 ||
		strings.Join(records[0], ",") != strings.Join(api.OutboundAIUserUsageExportColumns, ",") ||
		records[1][1] != "zed" || records[2][1] != "amy" || records[2][6] != "15" || records[2][7] != "7" {
		t.Fatalf("csv export = %d %q %q %v", code, contentType, body, err)
	}
	code, contentType, body = export("")
	var row api.OutboundAIUserUsageExportRow
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if code != http.StatusOK || !strings.HasPrefix(contentType, "application/x-ndjson") || len(lines) != 1 ||
		json.Unmarshal([]byte(lines[0]), &row) != nil || row.EndUser != "amy" || row.Tokens != 15 || row.UsageDate != today.Format("2006-01-02") {
		t.Fatalf("ndjson export = %d %q %q", code, contentType, body)
	}
	if code, _, body := export("?from=" + today.Format("2006-01-02") + "&to=" + today.Format("2006-01-02") + "&format=csv"); code != http.StatusOK || strings.Count(body, "\n") != 2 {
		t.Fatalf("one-day csv = %d %q", code, body)
	}
	for _, query := range []string{"?format=xml", "?from=2001-01-01", "?from=" + today.Format("2006-01-02") + "&to=" + yesterday} {
		if code, _, _ := export(query); code != http.StatusBadRequest {
			t.Errorf("export %q = %d", query, code)
		}
	}
	other := serve(service.ExportUserUsage, state.Account{ID: uuid.NewString()}, http.MethodGet, "/v1/outbound/integrations/x/ai-usage/users/export", integrationID, "")
	if other.Code != http.StatusNotFound {
		t.Fatalf("another account exported the usage: %d", other.Code)
	}
}

func TestListedAlertsNameTheEndUser(t *testing.T) {
	service, store, acct, integrationID := newTestService(t)
	store.SeedOutboundAIBudgetAlert(state.OutboundAIBudgetAlert{ID: uuid.NewString(), IntegrationID: integrationID,
		Budget: api.OutboundAIBudgetUserDailyTokens, EndUser: "user:1", PeriodStart: time.Now().UTC(), ThresholdPercent: 80,
		Limit: 100, Used: 80, CrossedAt: time.Now().UTC()})
	w := serve(service.ListBudgetAlerts, acct, http.MethodGet, "/v1/outbound/integrations/x/ai-alerts", integrationID, "")
	var list api.OutboundAIBudgetAlertList
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Items) != 1 ||
		list.Items[0].EndUser == nil || *list.Items[0].EndUser != "user:1" || list.Items[0].AppID != nil {
		t.Fatalf("alerts = %d %s", w.Code, w.Body.String())
	}
}
