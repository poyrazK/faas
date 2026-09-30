package state_test

import (
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgPlatformTenantStatementCoverageScale is an opt-in PostgreSQL load
// check for the largest supported statement window. It writes one 90-day,
// two-app statement (259,200 exact minute records), then calculates a one-unit
// late adjustment through the database-side coverage plan. It is intentionally
// kept out of the normal unit gate because it stores a production-shaped volume
// of coverage.
func TestPgPlatformTenantStatementCoverageScale(t *testing.T) {
	if os.Getenv("FAAS_RUN_PLATFORM_TENANT_COVERAGE_SCALE") != "1" {
		t.Skip("set FAAS_RUN_PLATFORM_TENANT_COVERAGE_SCALE=1 to run the PostgreSQL volume check")
	}

	store, pool, ctx := pgStoreWithPool(t)
	accountID, appA := seedConsumerKeyAccountApp(t, ctx, store)
	appBRecord, err := store.CreateApp(ctx, state.App{
		AccountID: accountID, Slug: "coverage-scale-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	appB := appBRecord.ID
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "coverage-scale-"+uuid.NewString()[:8], "Coverage scale", 250)
	if err != nil {
		t.Fatalf("CreatePlatformTenant: %v", err)
	}
	consumerA, err := store.CreateAPIConsumer(ctx, accountID, appA, "coverage-scale-a", "Coverage scale A")
	if err != nil {
		t.Fatalf("CreateAPIConsumer A: %v", err)
	}
	consumerB, err := store.CreateAPIConsumer(ctx, accountID, appB, "coverage-scale-b", "Coverage scale B")
	if err != nil {
		t.Fatalf("CreateAPIConsumer B: %v", err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, accountID, tenant.ID, consumerA.ID); err != nil {
		t.Fatalf("LinkPlatformTenantConsumer A: %v", err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, accountID, tenant.ID, consumerB.ID); err != nil {
		t.Fatalf("LinkPlatformTenantConsumer B: %v", err)
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const days = 90
	const apps = 2
	const minutesPerApp = days * 24 * 60
	const coverageCount = apps * minutesPerApp
	end := start.AddDate(0, 0, days)
	cards := make(map[string][]state.APIConsumerRateCard, apps)
	sources := []struct {
		appID    string
		consumer state.APIConsumer
		card     state.APIConsumerRateCard
	}{{appID: appA, consumer: consumerA}, {appID: appB, consumer: consumerB}}
	for i := range sources {
		card, err := store.CreateAPIConsumerRateCard(ctx, accountID, sources[i].appID, "EUR", 10, start)
		if err != nil {
			t.Fatalf("CreateAPIConsumerRateCard: %v", err)
		}
		sources[i].card = card
		cards[sources[i].appID] = []state.APIConsumerRateCard{card}
	}

	coverage := make([]state.PlatformTenantStatementCoverage, 0, coverageCount)
	for minuteIndex := 0; minuteIndex < minutesPerApp; minuteIndex++ {
		at := start.Add(time.Duration(minuteIndex) * time.Minute)
		for _, source := range sources {
			coverage = append(coverage, state.PlatformTenantStatementCoverage{
				AppID: source.appID, ConsumerID: source.consumer.ID, WindowStart: at, BillableUnits: 1,
			})
		}
	}
	encodedCoverage, err := json.Marshal(coverage)
	if err != nil {
		t.Fatalf("marshal coverage size: %v", err)
	}
	coverageJSONBytes := len(encodedCoverage)
	encodedCoverage = nil

	lineUnits := int64(minutesPerApp)
	lines := make([]state.PlatformTenantStatementLine, 0, apps)
	for _, source := range sources {
		lines = append(lines, state.PlatformTenantStatementLine{
			AppID: source.appID, ConsumerID: source.consumer.ID,
			WindowStart: start, WindowEnd: end, BillableUnits: lineUnits,
			RateCardID: source.card.ID, Currency: "EUR", PriceMillicentsPerUnit: 10,
			AmountMillicents: lineUnits * 10,
		})
	}
	input := state.PlatformTenantStatementInput{
		AccountID: accountID, TenantID: tenant.ID, PeriodStart: start, PeriodEnd: end,
		Revision: 1, Currency: "EUR", BillableUnits: coverageCount,
		AmountMillicents: int64(coverageCount) * 10, Lines: lines, Coverage: coverage, AsOf: end,
	}
	writeStarted := time.Now()
	statement, created, err := store.CreatePlatformTenantStatement(ctx, input)
	writeDuration := time.Since(writeStarted)
	if err != nil || !created {
		t.Fatalf("CreatePlatformTenantStatement: created=%t err=%v", created, err)
	}
	if len(statement.Lines) != apps || len(statement.Coverage) != 0 {
		t.Fatalf("initial scale statement header lines=%d coverage=%d", len(statement.Lines), len(statement.Coverage))
	}
	statementID := statement.ID
	input.Coverage = nil
	coverage = nil
	statement, changed, err := store.FinalizePlatformTenantStatement(ctx, accountID, tenant.ID, statementID)
	if err != nil || !changed {
		t.Fatalf("FinalizePlatformTenantStatement: changed=%t err=%v", changed, err)
	}

	var coverageTextBytes, coverageStorageBytes, lineStorageBytes, tupleBytes int64
	var statementRelationBytes int64
	if err := pool.QueryRow(ctx, `select octet_length(s.coverage::text)::bigint,
		pg_column_size(s.coverage)::bigint, pg_column_size(s.lines)::bigint, pg_column_size(s)::bigint,
		pg_total_relation_size('platform_tenant_statements'::regclass)::bigint
		from platform_tenant_statements s where s.id = $1::uuid`, statementID).Scan(
		&coverageTextBytes, &coverageStorageBytes, &lineStorageBytes, &tupleBytes, &statementRelationBytes); err != nil {
		t.Fatalf("measure statement storage: %v", err)
	}

	// Seed the already-aggregated minute table with CopyFrom so this measures
	// query/adjustment costs, not 259,200 independent event-ingestion commits.
	copyStarted := time.Now()
	copied, err := pool.CopyFrom(ctx, pgx.Identifier{"platform_tenant_usage_minutes"},
		[]string{"account_id", "platform_tenant_id", "app_id", "source_kind", "consumer_key", "window_start", "request_count", "error_count", "billable_units"},
		pgx.CopyFromSlice(coverageCount, func(i int) ([]any, error) {
			minuteIndex := i / apps
			source := sources[i%apps]
			at := start.Add(time.Duration(minuteIndex) * time.Minute)
			return []any{accountID, tenant.ID, source.appID, "consumer", source.consumer.ID, at, int64(1), int64(0), int64(1)}, nil
		}))
	copyDuration := time.Since(copyStarted)
	if err != nil || copied != coverageCount {
		t.Fatalf("seed tenant usage minutes: copied=%d err=%v", copied, err)
	}
	var usageRelationBytes int64
	if err := pool.QueryRow(ctx, `select pg_total_relation_size('platform_tenant_usage_minutes'::regclass)::bigint`).Scan(&usageRelationBytes); err != nil {
		t.Fatalf("measure usage storage: %v", err)
	}

	// Add exactly one late unit to an already-covered minute. The store plan
	// must return one unit without decoding the previous coverage or all usage.
	_, err = pool.Exec(ctx, `update platform_tenant_usage_minutes
		set request_count = request_count + 1, billable_units = billable_units + 1
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and app_id = $3::uuid
		and source_kind = 'consumer' and consumer_key = $4 and window_start = $5`,
		accountID, tenant.ID, appA, consumerA.ID, start)
	if err != nil {
		t.Fatalf("record late unit: %v", err)
	}

	runtime.GC()
	var beforePlan, afterPlan runtime.MemStats
	runtime.ReadMemStats(&beforePlan)
	planStarted := time.Now()
	plan, err := store.PlanPlatformTenantStatement(ctx, accountID, tenant.ID, start, end)
	planDuration := time.Since(planStarted)
	runtime.ReadMemStats(&afterPlan)
	planAlloc := afterPlan.TotalAlloc - beforePlan.TotalAlloc
	planHeapDelta := int64(afterPlan.HeapAlloc) - int64(beforePlan.HeapAlloc)
	if err != nil || !plan.HasLatest || plan.Latest.Revision != 1 || len(plan.Latest.Coverage) != 0 ||
		len(plan.UsageDelta) != 1 || plan.UsageDelta[0].BillableUnits != 1 || plan.UsageDelta[0].AppID != appA {
		t.Fatalf("late adjustment plan=%+v err=%v", plan, err)
	}

	runtime.GC()
	var beforeBuild, afterBuild runtime.MemStats
	runtime.ReadMemStats(&beforeBuild)
	buildStarted := time.Now()
	adjustment, err := billing.BuildPlatformTenantStatementFromDelta(accountID, tenant.ID, start, end, end.Add(time.Minute),
		plan.Latest.Revision+1, plan.Latest.Status, plan.UsageDelta, cards, nil)
	buildDuration := time.Since(buildStarted)
	runtime.ReadMemStats(&afterBuild)
	buildAlloc := afterBuild.TotalAlloc - beforeBuild.TotalAlloc
	buildHeapDelta := int64(afterBuild.HeapAlloc) - int64(beforeBuild.HeapAlloc)
	if err != nil || adjustment.Revision != 2 || adjustment.BillableUnits != 1 || len(adjustment.Coverage) != 1 ||
		len(adjustment.Lines) != 1 || adjustment.Lines[0].BillableUnits != 1 || adjustment.Lines[0].AppID != appA {
		t.Fatalf("late adjustment shape=%+v err=%v", adjustment, err)
	}
	adjustmentStored, created, err := store.CreatePlatformTenantStatement(ctx, adjustment)
	if err != nil || !created || len(adjustmentStored.Coverage) != 0 {
		t.Fatalf("persist adjustment: created=%t header_coverage=%d err=%v", created, len(adjustmentStored.Coverage), err)
	}
	if _, changed, err := store.FinalizePlatformTenantStatement(ctx, accountID, tenant.ID, adjustmentStored.ID); err != nil || !changed {
		t.Fatalf("finalize adjustment: changed=%t err=%v", changed, err)
	}
	replayPlan, err := store.PlanPlatformTenantStatement(ctx, accountID, tenant.ID, start, end)
	if err != nil || replayPlan.Latest.Revision != 2 || len(replayPlan.UsageDelta) != 0 {
		t.Fatalf("replay plan: revision=%d deltas=%d err=%v", replayPlan.Latest.Revision, len(replayPlan.UsageDelta), err)
	}
	if _, err := billing.BuildPlatformTenantStatementFromDelta(accountID, tenant.ID, start, end, end.Add(2*time.Minute),
		replayPlan.Latest.Revision+1, replayPlan.Latest.Status, replayPlan.UsageDelta, cards, nil); !errors.Is(err, billing.ErrNoNewTenantUsage) {
		t.Fatalf("replayed usage err=%v, want ErrNoNewTenantUsage", err)
	}

	var postgresVersion string
	if err := pool.QueryRow(ctx, `select version()`).Scan(&postgresVersion); err != nil {
		t.Fatalf("read postgres version: %v", err)
	}
	t.Logf("coverage scale: days=%d apps=%d minutes=%d lines=%d go_json_bytes=%d postgres_json_text_bytes=%d postgres_coverage_bytes=%d lines_bytes=%d row_bytes=%d statements_relation_bytes=%d usage_relation_bytes=%d",
		days, apps, coverageCount, len(lines), coverageJSONBytes, coverageTextBytes, coverageStorageBytes, lineStorageBytes, tupleBytes, statementRelationBytes, usageRelationBytes)
	t.Logf("coverage timings: statement_write=%s usage_fixture_copy=%s adjustment_plan=%s adjustment_build=%s; allocations_bytes={plan:%d build:%d}; heap_alloc_delta_before_gc_bytes={plan:%d build:%d}",
		writeDuration, copyDuration, planDuration, buildDuration,
		planAlloc, buildAlloc, planHeapDelta, buildHeapDelta)
	t.Logf("coverage backend: %s", postgresVersion)
}

func TestPgPlatformTenantStatementPlanDetectsCoverageRegression(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "coverage-regression-"+uuid.NewString()[:8], "Coverage regression", 250)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := store.CreateAPIConsumer(ctx, accountID, appID, "coverage-regression", "Coverage regression")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, accountID, tenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if applied, err := store.RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{
		EventID: uuid.NewString(), AccountID: accountID, AppID: appID, ConsumerKey: consumer.ID,
		PlatformTenantID: tenant.ID, WindowStart: start, RequestCount: 2, BillableUnits: 2,
	}); err != nil || !applied {
		t.Fatalf("RecordAPIConsumerUsage: applied=%t err=%v", applied, err)
	}
	input := state.PlatformTenantStatementInput{
		AccountID: accountID, TenantID: tenant.ID, PeriodStart: start, PeriodEnd: start.Add(time.Hour),
		Revision: 1, Currency: "EUR", BillableUnits: 2, AmountMillicents: 2, AsOf: start.Add(time.Hour),
		Lines: []state.PlatformTenantStatementLine{{AppID: appID, ConsumerID: consumer.ID,
			WindowStart: start, WindowEnd: start.Add(time.Minute), BillableUnits: 2,
			RateCardID: uuid.NewString(), Currency: "EUR", PriceMillicentsPerUnit: 1, AmountMillicents: 2}},
		Coverage: []state.PlatformTenantStatementCoverage{{AppID: appID, ConsumerID: consumer.ID,
			WindowStart: start, BillableUnits: 2}},
	}
	statement, created, err := store.CreatePlatformTenantStatement(ctx, input)
	if err != nil || !created {
		t.Fatalf("create statement: created=%t err=%v", created, err)
	}
	retry := input
	retry.Revision, retry.PriorStatus, retry.AsOf = 2, state.APIConsumerUsageStatementDraft, start.Add(2*time.Hour)
	replayed, created, err := store.CreatePlatformTenantStatement(ctx, retry)
	if err != nil || created || replayed.ID != statement.ID || replayed.Coverage != nil {
		t.Fatalf("identical draft replay: created=%t id=%s coverage=%d err=%v", created, replayed.ID, len(replayed.Coverage), err)
	}
	header, err := store.GetPlatformTenantStatementHeader(ctx, accountID, tenant.ID, statement.ID)
	if err != nil || header.Coverage != nil {
		t.Fatalf("statement header coverage=%d err=%v", len(header.Coverage), err)
	}
	headers, err := store.ListPlatformTenantStatementHeaders(ctx, accountID, tenant.ID, start, start.Add(time.Hour))
	if err != nil || len(headers) != 1 {
		t.Fatalf("statement header count=%d err=%v", len(headers), err)
	}
	if headers[0].Coverage != nil {
		t.Fatalf("statement list header included %d coverage records", len(headers[0].Coverage))
	}
	if _, changed, err := store.FinalizePlatformTenantStatement(ctx, accountID, tenant.ID, statement.ID); err != nil || !changed {
		t.Fatalf("finalize statement: changed=%t err=%v", changed, err)
	}
	if _, err := pool.Exec(ctx, `update platform_tenant_usage_minutes
		set request_count = 1, billable_units = 1
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and app_id = $3::uuid
		and source_kind = 'consumer' and consumer_key = $4 and window_start = $5`, accountID, tenant.ID, appID, consumer.ID, start); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PlanPlatformTenantStatement(ctx, accountID, tenant.ID, start, start.Add(time.Hour)); !errors.Is(err, state.ErrPlatformTenantUsageRegressed) {
		t.Fatalf("regressed usage plan err=%v, want ErrPlatformTenantUsageRegressed", err)
	}
}

func TestPgPlatformTenantStatementHandoffExcludesAppClaim(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "billing-tenant-"+uuid.NewString()[:8], "Billing tenant", 250)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := store.CreateAPIConsumer(ctx, accountID, appID, "billing-consumer", "Billing consumer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LinkPlatformTenantConsumer(ctx, accountID, tenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	makeTenantStatement := func(minute time.Time, revision int) state.PlatformTenantStatement {
		t.Helper()
		var priorStatus state.APIConsumerUsageStatementStatus
		if revision > 1 {
			priorStatus = state.APIConsumerUsageStatementFinalized
		}
		statement, created, err := store.CreatePlatformTenantStatement(ctx, state.PlatformTenantStatementInput{
			AccountID: accountID, TenantID: tenant.ID, PeriodStart: minute, PeriodEnd: minute.Add(time.Hour),
			Revision: revision, PriorStatus: priorStatus, Currency: "EUR", BillableUnits: 2, AmountMillicents: 20, AsOf: time.Now().UTC(),
			Lines: []state.PlatformTenantStatementLine{{AppID: appID, ConsumerID: consumer.ID, WindowStart: minute,
				BillableUnits: 2, RateCardID: uuid.NewString(), Currency: "EUR", PriceMillicentsPerUnit: 10, AmountMillicents: 20}},
		})
		if err != nil || !created {
			t.Fatalf("create tenant statement: %+v, %v, %v", statement, created, err)
		}
		if len(statement.Lines) != 1 || len(statement.Coverage) != 0 || !statement.Lines[0].WindowEnd.Equal(minute.Add(time.Minute)) {
			t.Fatalf("statement header round trip = %+v", statement)
		}
		stored, err := store.GetPlatformTenantStatement(ctx, accountID, tenant.ID, statement.ID)
		if err != nil || len(stored.Coverage) != 1 {
			t.Fatalf("statement coverage round trip: rows=%d err=%v", len(stored.Coverage), err)
		}
		statement, changed, err := store.FinalizePlatformTenantStatement(ctx, accountID, tenant.ID, statement.ID)
		if err != nil || !changed {
			t.Fatalf("finalize tenant statement: %+v, %v, %v", statement, changed, err)
		}
		return statement
	}
	first := makeTenantStatement(start, 1)
	claim := state.PlatformTenantStatementHandoffInput{AccountID: accountID, TenantID: tenant.ID,
		StatementID: first.ID, ExternalInvoiceID: "tenant-pg-invoice-1"}
	if _, created, err := store.CreatePlatformTenantStatementHandoff(ctx, claim); err != nil || !created {
		t.Fatalf("tenant claim: %v, %v", created, err)
	}
	if _, created, err := store.CreatePlatformTenantStatementHandoff(ctx, claim); err != nil || created {
		t.Fatalf("tenant claim replay: %v, %v", created, err)
	}
	adjustment := makeTenantStatement(start, 2)
	if _, created, err := store.CreatePlatformTenantStatementHandoff(ctx, state.PlatformTenantStatementHandoffInput{
		AccountID: accountID, TenantID: tenant.ID, StatementID: adjustment.ID, ExternalInvoiceID: "tenant-pg-adjustment",
	}); err != nil || !created {
		t.Fatalf("same-period adjustment handoff: %v, %v", created, err)
	}
	appStatement, _, err := store.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumer.ID, PeriodStart: start, PeriodEnd: start.Add(time.Hour),
		Currency: "EUR", BillableUnits: 2, AmountMillicents: 20, Priced: true, AsOf: time.Now().UTC(),
		Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: start, BillableUnits: 2, RateCardID: uuid.NewString(),
			Currency: "EUR", PriceMillicentsPerUnit: 10, AmountMillicents: 20}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, appStatement.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateAPIConsumerUsageStatementHandoff(ctx, state.APIConsumerUsageStatementHandoffInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumer.ID, StatementID: appStatement.ID,
		ExternalInvoiceID: "app-pg-overlap",
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("app overlap err=%v", err)
	}
	second := makeTenantStatement(start.Add(2*time.Hour), 1)
	if _, _, err := store.CreatePlatformTenantStatementHandoff(ctx, state.PlatformTenantStatementHandoffInput{
		AccountID: accountID, TenantID: tenant.ID, StatementID: second.ID, ExternalInvoiceID: claim.ExternalInvoiceID,
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("external invoice reuse err=%v", err)
	}
	if _, created, err := store.CreatePlatformTenantStatementHandoff(ctx, state.PlatformTenantStatementHandoffInput{
		AccountID: accountID, TenantID: tenant.ID, StatementID: second.ID, ExternalInvoiceID: "tenant-pg-invoice-2",
	}); err != nil || !created {
		t.Fatalf("non-overlapping tenant claim: %v, %v", created, err)
	}
	thirdStart := start.Add(4 * time.Hour)
	appFirst, _, err := store.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumer.ID, PeriodStart: thirdStart, PeriodEnd: thirdStart.Add(time.Hour),
		Currency: "EUR", BillableUnits: 2, AmountMillicents: 20, Priced: true, AsOf: time.Now().UTC(),
		Buckets: []state.APIConsumerUsageStatementBucket{{WindowStart: thirdStart, BillableUnits: 2, RateCardID: uuid.NewString(),
			Currency: "EUR", PriceMillicentsPerUnit: 10, AmountMillicents: 20}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.FinalizeAPIConsumerUsageStatement(ctx, accountID, appID, consumer.ID, appFirst.ID); err != nil {
		t.Fatal(err)
	}
	if _, created, err := store.CreateAPIConsumerUsageStatementHandoff(ctx, state.APIConsumerUsageStatementHandoffInput{
		AccountID: accountID, AppID: appID, ConsumerID: consumer.ID, StatementID: appFirst.ID,
		ExternalInvoiceID: "app-pg-first",
	}); err != nil || !created {
		t.Fatalf("app claim before tenant: %v, %v", created, err)
	}
	third := makeTenantStatement(thirdStart, 1)
	if _, _, err := store.CreatePlatformTenantStatementHandoff(ctx, state.PlatformTenantStatementHandoffInput{
		AccountID: accountID, TenantID: tenant.ID, StatementID: third.ID, ExternalInvoiceID: "tenant-pg-overlap",
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("tenant overlap err=%v", err)
	}
}

// adr: 239
func TestPgTenantSurfaceUsageStatementAndHandoff(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "surface-billing-"+uuid.NewString()[:8], "Surface billing", 250)
	if err != nil {
		t.Fatal(err)
	}
	surfaceID := uuid.NewString()
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	event := state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: accountID, AppID: appID,
		ConsumerKey: state.AnonymousConsumerKey, PlatformTenantID: tenant.ID,
		PlatformTenantSurfaceID: surfaceID, WindowStart: start, RequestCount: 3, BillableUnits: 3}
	if applied, err := store.RecordAPIConsumerUsage(ctx, event); err != nil || !applied {
		t.Fatalf("record surface event applied=%t err=%v", applied, err)
	}
	if applied, err := store.RecordAPIConsumerUsage(ctx, event); err != nil || applied {
		t.Fatalf("replay surface event applied=%t err=%v", applied, err)
	}
	minutes, err := store.ListPlatformTenantUsageMinutes(ctx, accountID, tenant.ID, start, start.Add(time.Minute))
	if err != nil || len(minutes) != 1 || minutes[0].SurfaceID != surfaceID || minutes[0].ConsumerKey != "" || minutes[0].BillableUnits != 3 {
		t.Fatalf("tenant surface minutes=%+v err=%v", minutes, err)
	}
	days, err := store.ListPlatformTenantUsage(ctx, accountID, tenant.ID, start, start.Add(time.Minute))
	if err != nil || len(days) != 1 || days[0].SurfaceID != surfaceID || days[0].BillableUnits != 3 {
		t.Fatalf("tenant surface days=%+v err=%v", days, err)
	}
	input := state.PlatformTenantStatementInput{AccountID: accountID, TenantID: tenant.ID,
		PeriodStart: start, PeriodEnd: start.Add(time.Hour), Revision: 1, Currency: "EUR",
		BillableUnits: 3, AmountMillicents: 30, AsOf: time.Now().UTC(),
		Lines: []state.PlatformTenantStatementLine{{AppID: appID, SurfaceID: surfaceID, WindowStart: start,
			BillableUnits: 3, RateCardID: uuid.NewString(), Currency: "EUR", PriceMillicentsPerUnit: 10, AmountMillicents: 30}}}
	statement, created, err := store.CreatePlatformTenantStatement(ctx, input)
	if err != nil || !created || len(statement.Lines) != 1 || len(statement.Coverage) != 1 || statement.Lines[0].SurfaceID != surfaceID || !statement.Lines[0].WindowEnd.Equal(start.Add(time.Minute)) {
		t.Fatalf("surface statement=%+v created=%t err=%v", statement, created, err)
	}
	if _, _, err := store.FinalizePlatformTenantStatement(ctx, accountID, tenant.ID, statement.ID); err != nil {
		t.Fatal(err)
	}
	if _, created, err := store.CreatePlatformTenantStatementHandoff(ctx, state.PlatformTenantStatementHandoffInput{
		AccountID: accountID, TenantID: tenant.ID, StatementID: statement.ID, ExternalInvoiceID: "surface-invoice-1",
	}); err != nil || !created {
		t.Fatalf("surface handoff created=%t err=%v", created, err)
	}
	other, _, err := store.CreatePlatformTenant(ctx, accountID, "surface-next-"+uuid.NewString()[:8], "Next owner", 250)
	if err != nil {
		t.Fatal(err)
	}
	input.TenantID = other.ID
	second, _, err := store.CreatePlatformTenantStatement(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.FinalizePlatformTenantStatement(ctx, accountID, other.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreatePlatformTenantStatementHandoff(ctx, state.PlatformTenantStatementHandoffInput{
		AccountID: accountID, TenantID: other.ID, StatementID: second.ID, ExternalInvoiceID: "surface-invoice-2",
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("overlapping surface handoff err=%v", err)
	}
}
