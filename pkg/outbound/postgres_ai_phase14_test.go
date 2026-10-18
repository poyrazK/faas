package outbound

// adr: 1436 — Postgres settles tagged calls into per-tag rows and the
// request log, admits against exact tags' daily cost limits with the live
// reservations of tagged leases, reads limits into the resolver, and
// checks tags in the database.

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresUsageTags(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{RequestLogEnabled: true})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetOutboundAITagLimits(ctx, f.account.ID, f.offer.ID, []api.OutboundAITagLimit{
		{Tag: "feature=search", DailyCostLimitMillicents: 100}, {Tag: "tier=free", DailyCostLimitMillicents: 1_000_000}}); err != nil {
		t.Fatal(err)
	}
	integration, err := f.integration(f.offer.ID)
	if err != nil || integration.AITagDailyCostLimits["feature=search"] != 100 || len(integration.AITagDailyCostLimits) != 2 {
		t.Fatalf("resolved tag limits = %v, %v", integration.AITagDailyCostLimits, err)
	}

	tags := []string{"feature=search", "tier=free"}
	spec := f.spec()
	spec.AITags, spec.AIReservedCostMillicents = tags, 60
	first, err := backend.Admit(ctx, spec)
	if err != nil || !first.Granted {
		t.Fatalf("first admission = %+v, %v", first, err)
	}
	// The live reservation of the first call counts against the limit.
	if refused, err := backend.Admit(ctx, spec); err != nil || refused.Granted || refused.Reason != ReasonTagDailyCostLimit {
		t.Fatalf("second admission = %+v, %v", refused, err)
	}
	record := AIUsageRecord{IntegrationID: f.offer.ID, Caller: f.app.ID, Outcome: AIUsageOutcomeKnown, Tags: tags,
		Usage: AIUsage{Model: "gpt-4o", Known: true, InputTokens: 20, OutputTokens: 10}, CostMillicents: 40,
		LeaseID: first.LeaseID, Log: &AIRequestLog{Status: http.StatusOK, Attempts: 1}}
	if _, err := backend.RecordAIUsageAlerts(ctx, record); err != nil {
		t.Fatal(err)
	}
	usage, err := f.store.GetOutboundAITagUsage(ctx, f.account.ID, f.offer.ID, "", "", 0)
	if err != nil || usage.TrackedTags != 2 || len(usage.Items) != 2 || usage.Items[0].CostMillicents != 40 ||
		usage.Items[0].DailyCostLimit == nil {
		t.Fatalf("tag usage = %+v, %v", usage, err)
	}
	if feature, err := f.store.GetOutboundAITagUsage(ctx, f.account.ID, f.offer.ID, "", "feature", 0); err != nil ||
		len(feature.Items) != 1 || feature.Items[0].Tag != "feature=search" || *feature.Items[0].DailyCostLimit != 100 {
		t.Fatalf("one key's tags = %+v, %v", feature, err)
	}
	logged, err := f.store.ListOutboundAIRequestLog(ctx, f.account.ID, f.offer.ID, nil, 10)
	if err != nil || len(logged) != 1 || len(logged[0].Tags) != 2 || logged[0].Tags[1] != "tier=free" {
		t.Fatalf("request log = %+v, %v", logged, err)
	}
	// 40 settled plus a 70 reservation passes 100; an untagged call does not
	// read the limit.
	spec.AIReservedCostMillicents = 70
	if refused, err := backend.Admit(ctx, spec); err != nil || refused.Granted || refused.Reason != ReasonTagDailyCostLimit {
		t.Fatalf("over the settled day = %+v, %v", refused, err)
	}
	spec.AITags = nil
	if granted, err := backend.Admit(ctx, spec); err != nil || !granted.Granted {
		t.Fatalf("untagged admission = %+v, %v", granted, err)
	}

	// The database refuses malformed tags.
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO outbound_ai_tag_usage_daily (integration_id, usage_date, tag) VALUES ($1, current_date, 'Bad=x')`, f.offer.ID); err == nil {
		t.Fatal("a malformed tag row was stored")
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_admission_leases SET ai_tags = ARRAY['a=1','b=2','c=3','d=4','e=5','f=6']`); err == nil {
		t.Fatal("a lease with six tags was stored")
	}
	if err := f.store.SetOutboundAITagLimits(ctx, f.account.ID, f.offer.ID, []api.OutboundAITagLimit{{Tag: "x", DailyCostLimitMillicents: 1}}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("an invalid limit = %v", err)
	}
	if err := f.store.SetOutboundAITagLimits(ctx, f.account.ID, f.offer.ID, nil); err != nil {
		t.Fatal(err)
	}
	if limits, err := f.store.ListOutboundAITagLimits(ctx, f.account.ID, f.offer.ID); err != nil || len(limits) != 0 {
		t.Fatalf("limits after clearing = %+v, %v", limits, err)
	}
}
