package outbound_test

// adr: 994 — Postgres admission caps one end user's UTC day with settled
// usage plus live reservations, and settlement writes the per-user rollup
// and the request log's end user.

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresEndUserDailyTokenLimit(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, fmt.Sprintf("outbound-ai-users-%s@example.com", uuid.NewString()), api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "ai-users-" + uuid.NewString()[:8], RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := store.CreateOutboundIntegration(ctx, state.OutboundIntegrationOffer{
		ID: uuid.NewString(), AccountID: account.ID, Name: "ai-users",
		Origin: "https://api.example.com", AllowedMethods: []string{http.MethodPost},
		AllowedPathPrefixes: []string{"/v1"}, Enabled: true,
		CredentialSource: outbound.CredentialSourceCustomerSealed, OwnerKind: outbound.IntegrationOwnerCustomer,
		RequestPolicy: api.OutboundRequestPolicy{RatePerSecond: 100, Burst: 20, MaxInFlight: 20, RequestTimeoutMS: 30_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BindOutboundIntegration(ctx, account.ID, app.ID, offer.ID); err != nil {
		t.Fatal(err)
	}
	userLimit := int64(60)
	if err := store.SetOutboundAIPolicy(ctx, account.ID, offer.ID, state.OutboundAIPolicy{
		Provider: api.OutboundAIProviderOpenAI, RequestLogEnabled: true, UserDailyTokenLimit: &userLimit,
	}); err != nil {
		t.Fatal(err)
	}
	resolver, err := outbound.NewPostgresResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := resolver.Integration(ctx, offer.ID); err != nil || resolved.AIUserDailyTokenLimit == nil || *resolved.AIUserDailyTokenLimit != userLimit {
		t.Fatalf("resolved user limit = %+v, %v", resolved.AIUserDailyTokenLimit, err)
	}
	backend, err := outbound.NewPostgresBackend(pool)
	if err != nil {
		t.Fatal(err)
	}
	admit := func(endUser string, reserve int64) outbound.Decision {
		t.Helper()
		decision, err := backend.Admit(ctx, outbound.AdmissionSpec{IntegrationID: offer.ID, RatePerSecond: 100, Burst: 20, MaxInFlight: 20,
			BindingAppID: app.ID, AIEndUser: endUser, AIReservedTokens: reserve, LeaseTTL: time.Minute})
		if err != nil {
			t.Fatalf("admit %s: %v", endUser, err)
		}
		return decision
	}
	held := admit("alice", 14)
	if !held.Granted {
		t.Fatalf("alice's first admission = %+v", held)
	}
	if decision := admit("alice", 50); decision.Granted || decision.Reason != outbound.ReasonUserDailyTokenLimit {
		t.Fatalf("alice past her live reservation = %+v", decision)
	}
	bob := admit("bob", 50)
	if !bob.Granted {
		t.Fatalf("bob was limited by alice's reservation: %+v", bob)
	}
	if err := backend.Release(ctx, offer.ID, bob.LeaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.RecordAIUsageAlerts(ctx, outbound.AIUsageRecord{IntegrationID: offer.ID, Caller: app.ID,
		Outcome: outbound.AIUsageOutcomeKnown, EndUser: "alice", CostMillicents: 7,
		Usage: outbound.AIUsage{Model: "gpt-4o", Known: true, InputTokens: 40, OutputTokens: 10},
		Log:   &outbound.AIRequestLog{Status: http.StatusOK, DurationMS: 12, Attempts: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := backend.Release(ctx, offer.ID, held.LeaseID); err != nil {
		t.Fatal(err)
	}
	if decision := admit("alice", 14); decision.Granted || decision.Reason != outbound.ReasonUserDailyTokenLimit {
		t.Fatalf("alice past her settled day = %+v", decision)
	}
	if decision := admit("", 14); !decision.Granted {
		t.Fatalf("a call without an end user = %+v", decision)
	}
	usage, err := store.GetOutboundAIUserUsage(ctx, account.ID, offer.ID, "", "", 10)
	if err != nil || usage.TrackedUsers != 1 || len(usage.Items) != 1 || usage.Items[0].EndUser != "alice" ||
		usage.Items[0].InputTokens != 40 || usage.Items[0].OutputTokens != 10 || usage.Items[0].CostMillicents != 7 ||
		usage.UserDailyTokenLimit == nil || *usage.UserDailyTokenLimit != userLimit {
		t.Fatalf("per-user usage = %+v, %v", usage, err)
	}
	if one, err := store.GetOutboundAIUserUsage(ctx, account.ID, offer.ID, "", "bob", 10); err != nil || len(one.Items) != 0 {
		t.Fatalf("bob settled nothing yet: %+v, %v", one, err)
	}
	if _, err := store.GetOutboundAIUserUsage(ctx, account.ID, offer.ID, "", "has space", 10); err == nil {
		t.Fatal("an invalid end user filter was accepted")
	}
	log, err := store.ListOutboundAIRequestLog(ctx, account.ID, offer.ID, nil, 10)
	if err != nil || len(log) != 1 || log[0].EndUser != "alice" {
		t.Fatalf("request log = %+v, %v", log, err)
	}
	offers, err := store.ListOutboundIntegrationOffers(ctx, account.ID)
	if err != nil || len(offers) != 1 || offers[0].AIUserDailyTokenLimit == nil || *offers[0].AIUserDailyTokenLimit != userLimit {
		t.Fatalf("offers = %+v, %v", offers, err)
	}
	// ADR-995: 50 of alice's 60 tokens records her 80% alert once.
	alerts, err := store.ListOutboundAIBudgetAlerts(ctx, account.ID, offer.ID, 10)
	if err != nil || len(alerts) != 1 || alerts[0].Budget != api.OutboundAIBudgetUserDailyTokens || alerts[0].EndUser != "alice" ||
		alerts[0].ThresholdPercent != 80 || alerts[0].AppID != "" || alerts[0].Used != 50 {
		t.Fatalf("alerts = %+v, %v", alerts, err)
	}
	var exported []state.OutboundAIUserUsageRow
	if err := store.ExportOutboundAIUserUsage(ctx, account.ID, offer.ID, "", "", func(row state.OutboundAIUserUsageRow) error {
		exported = append(exported, row)
		return nil
	}); err != nil || len(exported) != 1 || exported[0].EndUser != "alice" || exported[0].InputTokens != 40 {
		t.Fatalf("export = %+v, %v", exported, err)
	}
	if err := store.ExportOutboundAIUserUsage(ctx, account.ID, offer.ID, "2001-01-01", "", func(state.OutboundAIUserUsageRow) error { return nil }); err == nil {
		t.Fatal("an export outside the retention was accepted")
	}
	// ADR-996: once the day tracks the maximum number of end users, a new
	// end user is refused while per-user limits are set; tracked users are
	// still admitted.
	if _, err := pool.Exec(ctx, `
		INSERT INTO outbound_ai_user_usage_daily (integration_id, usage_date, end_user, requests)
		SELECT $1, (now() AT TIME ZONE 'UTC')::date, 'filler-' || n, 1 FROM generate_series(2, $2::int) AS n`,
		offer.ID, api.OutboundAIUsersPerDay); err != nil {
		t.Fatal(err)
	}
	if decision := admit("newcomer", 1); decision.Granted || decision.Reason != outbound.ReasonUserTrackingFull {
		t.Fatalf("a new end user beyond the tracking cap = %+v", decision)
	}
	if decision := admit("alice", 1); !decision.Granted {
		t.Fatalf("a tracked end user = %+v", decision)
	} else if err := backend.Release(ctx, offer.ID, decision.LeaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.PruneAIUsage(ctx); err != nil {
		t.Fatal(err)
	}
}
