package outbound

// adr: 1438 — Postgres settlement records a limited tag's 80% and 100%
// alerts once, naming the tag, and exports usage by tag over a day range.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresTagAlertsAndExport(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetOutboundAITagLimits(ctx, f.account.ID, f.offer.ID,
		[]api.OutboundAITagLimit{{Tag: "feature=search", DailyCostLimitMillicents: 100}}); err != nil {
		t.Fatal(err)
	}
	settle := func(cost int64) {
		t.Helper()
		record := AIUsageRecord{IntegrationID: f.offer.ID, Caller: f.app.ID, Outcome: AIUsageOutcomeKnown,
			Tags: []string{"feature=search", "tier=free"}, CostMillicents: cost,
			Usage: AIUsage{Model: "gpt-4o", Known: true, InputTokens: 20, OutputTokens: 10}}
		if _, err := backend.RecordAIUsageAlerts(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	settle(85) // 80%
	settle(10) // below 100%
	settle(10) // 100%
	settle(10) // already alerted
	alerts, err := f.store.ListOutboundAIBudgetAlerts(ctx, f.account.ID, f.offer.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var thresholds []int32
	for _, alert := range alerts {
		if alert.Budget != api.OutboundAIBudgetTagDailyCost {
			continue
		}
		if alert.Tag != "feature=search" || alert.Limit != 100 {
			t.Fatalf("tag alert = %+v", alert)
		}
		thresholds = append(thresholds, alert.ThresholdPercent)
	}
	if len(thresholds) != 2 {
		t.Fatalf("tag alert thresholds = %v (all %+v)", thresholds, alerts)
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO outbound_ai_budget_alerts (integration_id, account_id, budget, period_start, threshold_percent, limit_value, used_value)
		VALUES ($1, $2, 'tag_daily_cost', current_date, 80, 1, 1)`, f.offer.ID, f.account.ID); err == nil {
		t.Fatal("a tag alert without a tag was stored")
	}

	var rows []state.OutboundAITagUsageRow
	today := time.Now().UTC().Format("2006-01-02")
	if err := f.store.ExportOutboundAITagUsage(ctx, f.account.ID, f.offer.ID, today, today, "", func(row state.OutboundAITagUsageRow) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Tag != "feature=search" || rows[0].CostMillicents != 115 || rows[1].Tag != "tier=free" || rows[1].Requests != 4 {
		t.Fatalf("export rows = %+v", rows)
	}
}
