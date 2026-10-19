package outbound

// adr: 1340 — model canaries count settled route attempts per target only
// while they are active and roll back on worse errors; settled app calls
// reach the log-drain export outbox once, without content; and moderation
// checks may only name the account's own AI integrations.

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// secondAIIntegration adds another customer AI integration to the fixture's
// account.
func (f *aiPreviewFixture) secondAIIntegration(provider string) state.OutboundIntegrationOffer {
	f.t.Helper()
	ctx := context.Background()
	offer, err := f.store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: f.account.ID, Name: "llm-" + uuid.NewString()[:6], Origin: f.upstream.URL,
		AllowedMethods: []string{http.MethodPost}, AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: CredentialSourceCustomerSealed, OwnerKind: IntegrationOwnerCustomer,
		RequestPolicy: api.OutboundRequestPolicy{RatePerSecond: 100, Burst: 20, MaxInFlight: 20, RequestTimeoutMS: 30_000},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.SetOutboundAIPolicy(ctx, f.account.ID, offer.ID, state.OutboundAIPolicy{Provider: provider}); err != nil {
		f.t.Fatal(err)
	}
	return offer
}

// integration resolves one of the fixture's integrations from Postgres.
func (f *aiPreviewFixture) integration(id string) (Integration, error) {
	resolver, err := NewPostgresResolver(f.pool)
	if err != nil {
		return Integration{}, err
	}
	return resolver.Integration(context.Background(), id)
}

func routeAttemptSettlement(f *aiPreviewFixture, routeID string, position int, failed bool) AIUsageRecord {
	record := AIUsageRecord{IntegrationID: f.offer.ID, Caller: f.app.ID, Outcome: AIUsageOutcomeKnown,
		Usage:          AIUsage{Model: "gpt-4o", Known: true, InputTokens: 10, OutputTokens: 5},
		CostMillicents: 3, Route: &AIRouteAttemptRecord{RouteID: routeID, Position: position}}
	if failed {
		record.Outcome, record.Usage, record.CostMillicents, record.Route.Error = AIUsageOutcomeError, AIUsage{Model: "gpt-4o"}, 0, true
	} else {
		ttfb := int64(100 * position)
		record.Route.TTFBMS = &ttfb
	}
	return record
}

func TestPostgresRouteCanaryLifecycle(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	ctx := context.Background()
	second := f.secondAIIntegration(api.OutboundAIProviderOpenAI)
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewPostgresRouteResolver(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	route, err := f.store.CreateOutboundAIRoute(ctx, state.OutboundAIRoute{ID: uuid.NewString(), AccountID: f.account.ID, Name: "chat",
		Targets: []state.OutboundAIRouteTarget{{IntegrationID: f.offer.ID}, {IntegrationID: second.ID, Model: "gpt-new"}}})
	if err != nil {
		t.Fatal(err)
	}
	// Without a canary, settlements write no rows.
	if _, err := backend.RecordAIUsageAlerts(ctx, routeAttemptSettlement(f, route.ID, 1, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SetOutboundAIRouteCanary(ctx, f.account.ID, route.ID, state.OutboundAIRouteCanary{Position: 3, Percent: 10,
		MaxErrorRateIncrease: 5, MinCalls: 10, WindowMinutes: 30}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("a canary past the last target = %v", err)
	}
	canary, err := f.store.SetOutboundAIRouteCanary(ctx, f.account.ID, route.ID, state.OutboundAIRouteCanary{Position: 2, Percent: 25,
		MaxErrorRateIncrease: 5, MinCalls: 10, WindowMinutes: 30})
	if err != nil || canary.State != api.OutboundAIRouteCanaryActive {
		t.Fatalf("SetOutboundAIRouteCanary = %+v, %v", canary, err)
	}
	loaded, err := resolver.Route(ctx, route.ID)
	if err != nil || loaded.Canary == nil || *loaded.Canary != (AIRouteCanary{Position: 2, Percent: 25}) {
		t.Fatalf("resolved route = %+v, %v", loaded, err)
	}
	for index := range 20 {
		if _, err := backend.RecordAIUsageAlerts(ctx, routeAttemptSettlement(f, route.ID, 1, false)); err != nil {
			t.Fatal(err)
		}
		if index < 10 {
			if _, err := backend.RecordAIUsageAlerts(ctx, routeAttemptSettlement(f, route.ID, 2, index%2 == 0)); err != nil {
				t.Fatal(err)
			}
		}
	}
	status, err := f.store.OutboundAIRouteCanaryStatus(ctx, f.account.ID, route.ID)
	if err != nil || status.Baseline.Calls != 20 || status.Baseline.Errors != 0 || status.Baseline.TTFBCalls != 20 ||
		status.Baseline.TTFBMSTotal != 2000 || status.CanaryStats.Calls != 10 || status.CanaryStats.Errors != 5 ||
		status.CanaryStats.CostMillicents != 15 {
		t.Fatalf("canary status = %+v, %v", status, err)
	}
	evaluator := &AIRouteCanaryEvaluator{Store: backend}
	if rolledBack, err := evaluator.EvaluateOnce(ctx); err != nil || rolledBack != 1 {
		t.Fatalf("EvaluateOnce = %d, %v", rolledBack, err)
	}
	stored, err := f.store.GetOutboundAIRoute(ctx, f.account.ID, route.ID)
	if err != nil || stored.Canary == nil || stored.Canary.State != api.OutboundAIRouteCanaryRolledBack ||
		stored.Canary.Reason != api.OutboundAIRouteCanaryReasonErrorRate || stored.Canary.DecidedAt == nil {
		t.Fatalf("rolled-back route = %+v, %v", stored.Canary, err)
	}
	if loaded, err := resolver.Route(ctx, route.ID); err != nil || loaded.Canary != nil {
		t.Fatalf("a rolled-back canary still routes: %+v, %v", loaded.Canary, err)
	}
	if _, err := backend.RecordAIUsageAlerts(ctx, routeAttemptSettlement(f, route.ID, 2, true)); err != nil {
		t.Fatal(err)
	}
	if status, _ := f.store.OutboundAIRouteCanaryStatus(ctx, f.account.ID, route.ID); status.CanaryStats.Calls != 10 {
		t.Fatalf("a rolled-back canary kept counting: %+v", status.CanaryStats)
	}
	if rolledBack, err := evaluator.EvaluateOnce(ctx); err != nil || rolledBack != 0 {
		t.Fatalf("a second evaluation = %d, %v", rolledBack, err)
	}

	// Promote moves the canary target first and ends the canary.
	if _, err := f.store.SetOutboundAIRouteCanary(ctx, f.account.ID, route.ID, state.OutboundAIRouteCanary{Position: 2, Percent: 50,
		MaxErrorRateIncrease: 5, MinCalls: 10, WindowMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.RecordAIUsageAlerts(ctx, routeAttemptSettlement(f, route.ID, 2, false)); err != nil {
		t.Fatal(err)
	}
	promoted, err := f.store.PromoteOutboundAIRouteCanary(ctx, f.account.ID, route.ID)
	if err != nil || promoted.Canary != nil || promoted.Targets[0].IntegrationID != second.ID || promoted.Targets[0].Model != "gpt-new" ||
		promoted.Targets[1].IntegrationID != f.offer.ID {
		t.Fatalf("promoted route = %+v, %v", promoted, err)
	}
	if _, err := f.store.PromoteOutboundAIRouteCanary(ctx, f.account.ID, route.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("promoting without a canary = %v", err)
	}
	assertRouteRows(t, f, route.ID, 0)

	// New targets end a canary; deleting the route deletes its rows.
	if _, err := f.store.SetOutboundAIRouteCanary(ctx, f.account.ID, route.ID, state.OutboundAIRouteCanary{Position: 2, Percent: 50,
		MaxErrorRateIncrease: 5, MinCalls: 10, WindowMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetOutboundAIRouteTargets(ctx, f.account.ID, route.ID, promoted.Targets, state.OutboundAIRouteRouting{}); err != nil {
		t.Fatal(err)
	}
	if stored, _ := f.store.GetOutboundAIRoute(ctx, f.account.ID, route.ID); stored.Canary != nil {
		t.Fatalf("new targets kept the canary: %+v", stored.Canary)
	}
	if _, err := f.store.SetOutboundAIRouteCanary(ctx, f.account.ID, route.ID, state.OutboundAIRouteCanary{Position: 2, Percent: 50,
		MaxErrorRateIncrease: 5, MinCalls: 10, WindowMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.RecordAIUsageAlerts(ctx, routeAttemptSettlement(f, route.ID, 1, false)); err != nil {
		t.Fatal(err)
	}
	assertRouteRows(t, f, route.ID, 1)
	if err := f.store.DeleteOutboundAIRoute(ctx, f.account.ID, route.ID); err != nil {
		t.Fatal(err)
	}
	assertRouteRows(t, f, route.ID, 0)
	// A settlement for a deleted route still settles.
	if _, err := backend.RecordAIUsageAlerts(ctx, routeAttemptSettlement(f, route.ID, 1, false)); err != nil {
		t.Fatalf("a settlement for a deleted route failed: %v", err)
	}
}

func assertRouteRows(t *testing.T, f *aiPreviewFixture, routeID string, want int) {
	t.Helper()
	var rows int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM outbound_ai_route_target_minutes WHERE route_id = $1`,
		routeID).Scan(&rows); err != nil || rows != want {
		t.Fatalf("route %s has %d per-minute rows, want %d (%v)", routeID, rows, want, err)
	}
}

func TestPostgresAILogExportOutbox(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{LogExport: true})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	integration, err := f.integration(f.offer.ID)
	if err != nil || !integration.AILogExport {
		t.Fatalf("resolved integration export = %v, %v", integration.AILogExport, err)
	}
	decision, err := backend.Admit(ctx, f.spec())
	if err != nil || !decision.Granted {
		t.Fatalf("admission = %+v, %v", decision, err)
	}
	record := AIUsageRecord{IntegrationID: f.offer.ID, Caller: f.app.ID, Outcome: AIUsageOutcomeKnown,
		Usage: AIUsage{Model: "gpt-4o", Known: true, InputTokens: 20, OutputTokens: 10}, LeaseID: decision.LeaseID,
		Export: &AILogExport{AppID: f.app.ID, AccountID: f.account.ID, Line: `{"source":"ai_gateway","model":"gpt-4o"}`,
			Attributes: map[string]string{"gen_ai.system": "openai"}}}
	for range 2 {
		if _, err := backend.RecordAIUsageAlerts(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	if fresh, err := f.store.ListAIGatewayLogExports(ctx, f.app.ID, 0, time.Time{}, 15*time.Second, 10); err != nil || len(fresh) != 0 {
		t.Fatalf("a line younger than the visibility horizon was listed: %+v, %v", fresh, err)
	}
	exports, err := f.store.ListAIGatewayLogExports(ctx, f.app.ID, 0, time.Time{}, 0, 10)
	if err != nil || len(exports) != 1 || exports[0].AccountID != f.account.ID || exports[0].Line != record.Export.Line ||
		exports[0].Attributes["gen_ai.system"] != "openai" {
		t.Fatalf("exports after a retried settlement = %+v, %v", exports, err)
	}
	if after, err := f.store.ListAIGatewayLogExports(ctx, f.app.ID, exports[0].ID, time.Time{}, 0, 10); err != nil || len(after) != 0 {
		t.Fatalf("lines after the cursor = %+v, %v", after, err)
	}
	if later, err := f.store.ListAIGatewayLogExports(ctx, f.app.ID, 0, time.Now().Add(time.Minute), 0, 10); err != nil || len(later) != 0 {
		t.Fatalf("lines before a fresh cursor's start = %+v, %v", later, err)
	}
	if other, err := f.store.ListAIGatewayLogExports(ctx, uuid.NewString(), 0, time.Time{}, 0, 10); err != nil || len(other) != 0 {
		t.Fatalf("another app's lines = %+v, %v", other, err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_log_exports SET created_at = now() - make_interval(hours => $1::int + 1)`,
		api.OutboundAILogExportRetentionHours); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.PruneAIUsage(ctx); err != nil {
		t.Fatal(err)
	}
	if left, _ := f.store.ListAIGatewayLogExports(ctx, f.app.ID, 0, time.Time{}, 0, 10); len(left) != 0 {
		t.Fatalf("%d export lines outlived the retention", len(left))
	}
}

func TestPostgresModerationPolicyNamesOwnIntegrations(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	ctx := context.Background()
	openai := f.secondAIIntegration(api.OutboundAIProviderOpenAI)
	anthropic := f.secondAIIntegration(api.OutboundAIProviderAnthropic)
	other := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	policy := func(kind, integrationID string) api.OutboundAIContentPolicy {
		moderation := api.OutboundAIModeration{Action: api.OutboundAIModerationBlock, Kind: kind, IntegrationID: integrationID,
			Categories: []string{api.OutboundAIModerationHate}, OnError: api.OutboundAIModerationOnErrorBlock}
		if kind == api.OutboundAIModerationKindClassifier {
			moderation.Model = "claude-haiku"
		}
		return api.OutboundAIContentPolicy{Moderation: moderation}
	}
	for _, tc := range []struct {
		name   string
		policy api.OutboundAIContentPolicy
		want   error
	}{
		{"openai moderation on openai", policy(api.OutboundAIModerationKindOpenAI, openai.ID), nil},
		{"openai moderation on anthropic", policy(api.OutboundAIModerationKindOpenAI, anthropic.ID), state.ErrInvalidArgument},
		{"classifier on anthropic", policy(api.OutboundAIModerationKindClassifier, anthropic.ID), nil},
		{"another account's integration", policy(api.OutboundAIModerationKindClassifier, other.offer.ID), state.ErrInvalidArgument},
		{"a missing integration", policy(api.OutboundAIModerationKindClassifier, uuid.NewString()), state.ErrInvalidArgument},
	} {
		if err := f.store.SetOutboundAIContentPolicy(ctx, f.account.ID, f.offer.ID, tc.policy); !errors.Is(err, tc.want) {
			t.Errorf("%s: SetOutboundAIContentPolicy = %v, want %v", tc.name, err, tc.want)
		}
	}
	integration, err := f.integration(f.offer.ID)
	moderation := integration.AIContentPolicy.Moderation
	if err != nil || moderation.Kind != api.OutboundAIModerationKindClassifier || moderation.IntegrationID != anthropic.ID ||
		moderation.Model != "claude-haiku" || moderation.OnError != api.OutboundAIModerationOnErrorBlock || len(moderation.Categories) != 1 {
		t.Fatalf("resolved moderation = %+v, %v", moderation, err)
	}
	offers, err := f.store.ListOutboundIntegrationOffers(ctx, f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, offer := range offers {
		if offer.ID == f.offer.ID && offer.AIContentPolicy.Moderation.IntegrationID != anthropic.ID {
			t.Fatalf("listed moderation = %+v", offer.AIContentPolicy.Moderation)
		}
	}
	// Turning AI handling off clears the check, and deleting the moderation
	// integration leaves a check that fails closed or open at runtime.
	if err := f.store.SetOutboundAIPolicy(ctx, f.account.ID, f.offer.ID, state.OutboundAIPolicy{}); err != nil {
		t.Fatal(err)
	}
	var action string
	if err := f.pool.QueryRow(ctx, `SELECT ai_moderation_action FROM outbound_integrations WHERE id = $1`, f.offer.ID).Scan(&action); err != nil ||
		action != api.OutboundAIModerationOff {
		t.Fatalf("moderation after AI was turned off = %q, %v", action, err)
	}
}
