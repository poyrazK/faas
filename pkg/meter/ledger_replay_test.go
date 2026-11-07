// adr: 20261019193600, 566, 20261101143527, 20261103131742
//
// Ledger replay: over a closed UTC month, the compute and egress the pusher
// delivered equal what the financial ledger bills for every paid billing
// subject, through plan changes, a late subscription, an organization, the
// egress overage switch and a month boundary, on the memory store and on
// Postgres. This is the scenario evidence that gates moving delivery to the
// ledger.
package meter_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
)

// replayEvent happens at a minute: a plan change, an egress overage switch,
// or usage.
type replayEvent struct {
	at        time.Time
	subject   string
	plan      api.Plan // a plan change when set
	overage   string   // "on" or "off": the egress overage switch
	gbHours   int64    // compute usage
	egressGiB int64    // egress usage
}

const gib = int64(1) << 30

func gbh(n int64) int64 { return n * api.SecondsPerGBHour }

// replayBackend is a store and the test seams the replay needs from it.
type replayBackend struct {
	store state.Store
	// markComplete records the minutes in [from, to) as fully sampled, for
	// compute and egress.
	markComplete func(t *testing.T, from, to time.Time)
	// bindOrg binds an organization to its Polar customer and subscription,
	// billing usage from from on.
	bindOrg func(t *testing.T, orgID string, from time.Time)
}

func TestLedgerReplayClosedMonthMatches(t *testing.T) {
	for _, source := range []meter.UsageSource{meter.UsageSourceOff, meter.UsageSourceLedgerShadow, meter.UsageSourceLedger} {
		t.Run("mem/"+string(source), func(t *testing.T) { replayMem(t, source) })
	}
	for _, source := range []meter.UsageSource{meter.UsageSourceLedgerShadow, meter.UsageSourceLedger} {
		t.Run("postgres/"+string(source), func(t *testing.T) { replayPostgres(t, source) })
	}
}

func replayMem(t *testing.T, source meter.UsageSource) {
	mem := state.NewMemStore()
	replayLedgerMonth(t, source, replayBackend{
		store: statetest.NewMem(ledgerRetention{MemStore: mem}),
		markComplete: func(t *testing.T, from, to time.Time) {
			t.Helper()
			for minute := from; minute.Before(to); minute = minute.Add(time.Minute) {
				if err := mem.RecordFinancialSamplingWindow(context.Background(), minute, true, true); err != nil {
					t.Fatal(err)
				}
			}
		},
		bindOrg: func(t *testing.T, orgID string, from time.Time) {
			t.Helper()
			if err := mem.UpsertBillingIdentity(context.Background(), state.BillingIdentity{AccountID: orgID, Provider: "polar",
				CustomerID: "cus-" + orgID, SubscriptionID: "sub-" + orgID, BillingFrom: from}); err != nil {
				t.Fatal(err)
			}
		},
	})
}

func replayPostgres(t *testing.T, source meter.UsageSource) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// The ledger keeps evidence for the whole replayed year.
	if _, err := pool.Exec(ctx, `UPDATE financial_evidence_coverage SET retained_from = $1 WHERE singleton`,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	replayLedgerMonth(t, source, replayBackend{
		store: store,
		markComplete: func(t *testing.T, from, to time.Time) {
			t.Helper()
			if _, err := pool.Exec(ctx, `
					INSERT INTO financial_sampling_windows(minute, compute_complete, egress_complete)
					SELECT m, true, true FROM generate_series($1::timestamptz, $2::timestamptz - interval '1 minute', interval '1 minute') AS m
					ON CONFLICT(minute) DO UPDATE SET compute_complete = true, egress_complete = true`, from, to); err != nil {
				t.Fatalf("mark %s..%s complete: %v", from, to, err)
			}
		},
		// An organization binds through its own customer and
		// subscription columns, as the provider webhooks do; the
		// binding bills from the moment it is made, so the replay
		// moves it to the start of its billing.
		bindOrg: func(t *testing.T, orgID string, from time.Time) {
			t.Helper()
			if err := store.UpdateAccountProviderCustomerID(ctx, orgID, "cus-"+orgID); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateAccountStripeSubscriptionItem(ctx, orgID, "sub-"+orgID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE billing_identities SET billing_from = $2 WHERE account_id = $1 AND provider = 'polar'`,
				orgID, from); err != nil {
				t.Fatal(err)
			}
		},
	})
}

// replayLedgerMonth replays October 2026 for eight paid billing subjects
// against the backend, delivering from source through a real Polar provider
// as the hourly pusher does, then reconciles the closed month.
func replayLedgerMonth(t *testing.T, source meter.UsageSource, backend replayBackend) {
	ctx := context.Background()
	store, markComplete := backend.store, backend.markComplete
	deliveries, ok := store.(state.BillingDeliveryReader)
	if !ok {
		t.Fatalf("%T reads no delivery receipts", store)
	}
	switches, ok := store.(state.EgressOverageStore)
	if !ok {
		t.Fatalf("%T keeps no egress overage elections", store)
	}
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := month.AddDate(0, 1, 0)
	day := func(d, h int) time.Time { return month.AddDate(0, 0, d-1).Add(time.Duration(h) * time.Hour) }
	// A real Polar provider against a fake API: delivery is keyed by each
	// subject's Polar billing identity, as in production. Egress bills live
	// from the 5th, with 10 GiB included on Hobby and 50 on Pro.
	polarAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/events/ingest" {
			_, _ = io.WriteString(w, `{"inserted":1,"duplicates":0}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(polarAPI.Close)
	provider, err := polar.NewProvider(polar.Config{APIKey: "polar-replay-token", WebhookSecret: "polar_whs_replay",
		BaseURL: polarAPI.URL, UsageEventName: "ram_usage", MeterID: "meter-1",
		EgressBillingMode: polar.EgressBillingLive, EgressBillingFrom: day(5, 0).Format(time.RFC3339), EgressMeterID: "egress-meter-1",
		HobbyIncludedEgressGiB: 10, ProIncludedEgressGiB: 50, ScaleIncludedEgressGiB: 100}, nil)
	if err != nil {
		t.Fatal(err)
	}

	subjects := map[string]string{}
	account := func(name string, plan api.Plan) string {
		t.Helper()
		acct, err := store.CreateAccount(ctx, "replay-"+name+"@example.com", plan)
		if err != nil {
			t.Fatal(err)
		}
		subjects[name] = acct.ID
		return acct.ID
	}
	identity := func(id string, from time.Time) {
		t.Helper()
		if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: id, Provider: "polar", CustomerID: "cus-" + id,
			SubscriptionID: "sub-" + id, BillingFrom: from}); err != nil {
			t.Fatal(err)
		}
	}
	paid := func(name string, plan api.Plan) {
		identity(account(name, plan), time.Unix(0, 0).UTC())
	}
	paid("steady", api.PlanHobby)
	paid("upgrade", api.PlanHobby)
	paid("downgrade", api.PlanPro)
	paid("return", api.PlanHobby)
	paid("boundary", api.PlanHobby)
	paid("capped", api.PlanHobby)
	fiftyCents := int64(50)
	if err := store.UpdateAccountOverageCapCents(ctx, subjects["capped"], &fiftyCents); err != nil {
		t.Fatal(err)
	}
	// Subscribes on the 12th: usage before is Free and never billed.
	account("late", api.PlanFree)
	// A company organization is its own billing subject.
	org, err := store.CreateOrg(ctx, state.Org{Slug: "replay-co", Name: "Replay Co", Plan: api.PlanFree, Status: state.OrgStatusActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateOrgPlan(ctx, org.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	backend.bindOrg(t, org.ID, time.Unix(0, 0).UTC())
	subjects["org"] = org.ID
	apps := map[string][2]string{}
	for name := range subjects {
		apps[name] = [2]string{uuid.NewString(), uuid.NewString()}
	}

	events := []replayEvent{
		{at: month.Add(-time.Minute), subject: "boundary", gbHours: 500}, // September: not October's
		{at: month, subject: "boundary", gbHours: 260},
		{at: month.Add(-time.Hour), subject: "steady", overage: "on"},
		// Before egress billing starts on the 5th: neither billed nor counted.
		{at: day(2, 11), subject: "steady", egressGiB: 4},
		{at: day(9, 11), subject: "steady", egressGiB: 6},
		{at: day(21, 11), subject: "steady", egressGiB: 8},
		// Overage off: usage above the allowance is never billed.
		{at: day(9, 11), subject: "upgrade", egressGiB: 12},
		{at: day(21, 11), subject: "upgrade", egressGiB: 8},
		// Overage turned on after 5 GiB above the allowance went unbilled:
		// those are not billed later.
		{at: day(9, 11), subject: "org", egressGiB: 15},
		{at: day(10, 0), subject: "org", overage: "on"},
		{at: day(14, 11), subject: "org", egressGiB: 3},
		// Free egress counts toward the allowance and is never billed.
		{at: day(9, 11), subject: "late", egressGiB: 8},
		{at: day(12, 0), subject: "late", overage: "on"},
		{at: day(14, 11), subject: "late", egressGiB: 6},
		// Overage off: the unbilled egress spends none of the 50-cent cap.
		{at: day(9, 11), subject: "capped", egressGiB: 15},
	}
	for _, name := range []string{"steady", "upgrade", "downgrade", "return", "capped", "late", "org"} {
		for _, d := range []int{2, 9, 14, 21, 28} {
			events = append(events, replayEvent{at: day(d, 10), subject: name, gbHours: 70})
		}
	}
	events = append(events,
		replayEvent{at: day(15, 0), subject: "upgrade", plan: api.PlanPro},
		replayEvent{at: day(15, 0), subject: "downgrade", plan: api.PlanHobby},
		replayEvent{at: day(10, 0), subject: "return", plan: api.PlanPro},
		replayEvent{at: day(20, 0), subject: "return", plan: api.PlanHobby},
		replayEvent{at: day(12, 0), subject: "late", plan: api.PlanHobby},
	)
	sort.SliceStable(events, func(i, j int) bool { return events[i].at.Before(events[j].at) })

	ops := wire.NewOpsMetrics("meter_test_replay")
	marked := month.Add(-time.Hour)
	checkpoint := func(at time.Time) {
		t.Helper()
		markComplete(t, marked, at)
		marked = at
		p := meter.NewPusher(store, provider, discardLog(), func() time.Time { return at }, ops).WithUsageSource(source)
		if _, err := p.PushPending(ctx, 40*24*time.Hour); err != nil {
			t.Fatalf("push at %s: %v", at, err)
		}
	}
	lastCheckpoint := month.Add(-time.Hour)
	for _, ev := range events {
		// Deliver what was final before this event, as the hourly pusher does.
		if ev.at.Sub(lastCheckpoint) >= 24*time.Hour {
			checkpoint(ev.at.Truncate(time.Hour))
			lastCheckpoint = ev.at
		}
		id := subjects[ev.subject]
		if ev.plan != "" {
			if ev.subject == "org" {
				err = store.UpdateOrgPlan(ctx, id, ev.plan)
			} else {
				err = store.UpdateAccountPlan(ctx, id, ev.plan)
			}
			if err != nil {
				t.Fatal(err)
			}
			if ev.subject == "late" {
				identity(id, ev.at)
			}
		}
		if ev.overage != "" {
			if _, err := switches.SetEgressOverage(ctx, id, ev.overage == "on", ev.at); err != nil {
				t.Fatal(err)
			}
		}
		if err := meter.RecordFinancialPricingForTest(ctx, store, provider, ev.at); err != nil {
			t.Fatalf("pricing at %s: %v", ev.at, err)
		}
		if ev.gbHours > 0 || ev.egressGiB > 0 {
			app := apps[ev.subject]
			if err := store.AppendUsage(ctx, id, app[0], app[1], ev.at, gbh(ev.gbHours), 0, 0, 0, ev.egressGiB*gib, 0, 0, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The month closed; the pusher delivers its last hours.
	checkpoint(end.Add(2 * time.Hour))

	clocked := meter.NewPusher(store, provider, discardLog(), func() time.Time { return end.Add(2 * time.Hour) }, nil).WithUsageSource(source)
	reconciler := meter.NewLedgerReconciler(clocked, "polar", prometheus.NewRegistry())
	passes, err := reconciler.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(passes) == 0 || passes[0].Month != "previous" || !passes[0].Start.Equal(month) {
		t.Fatalf("passes = %+v, want October as the previous month", passes)
	}
	october := passes[0]
	// The default Hobby cap ($2, 200 GB-h) bounds late's compute, and
	// capped's own cap of 50 cents bounds it; every other subject matches.
	want := map[string]int{meter.LedgerMatch: len(subjects) - 2, meter.LedgerCapped: 2}
	if len(october.Results) != len(want) || october.Results[meter.LedgerMatch] != want[meter.LedgerMatch] || october.Results[meter.LedgerCapped] != want[meter.LedgerCapped] {
		t.Errorf("October compute results = %v (drift %d MB-s), want %v", october.Results, october.DriftMBSeconds, want)
	}
	if got := october.EgressResults; len(got) != 1 || got[meter.LedgerMatch] != len(subjects) {
		t.Errorf("October egress results = %v (drift %d bytes), want every subject matched", got, october.EgressDriftBytes)
	}

	// Both sources agree on every hour, and each receipt names the source
	// that billed it.
	compared := counterTotal(t, ops.Registry(), "meterd_billing_usage_source_compared_windows_total")
	if mismatched := counterTotal(t, ops.Registry(), "meterd_billing_usage_source_mismatch_windows_total"); mismatched != 0 {
		t.Errorf("%v of %v compared windows disagreed", mismatched, compared)
	}
	if want := source != meter.UsageSourceOff; (compared > 0) != want {
		t.Errorf("compared windows = %v; want some only when comparing (%s)", compared, source)
	}
	receipt := state.UsageSourceUsageMinutes
	if source == meter.UsageSourceLedger {
		receipt = state.UsageSourceLedger
	}
	if recorder, ok := store.(state.BillingDeliverySourceRecorder); !ok {
		t.Errorf("%T records no receipt source", store)
	} else if got, err := recorder.BillingDeliverySource(ctx, "polar", subjects["steady"], state.BillingMeterCompute, day(21, 10)); err != nil || got != receipt {
		t.Errorf("steady's day-21 receipt source = %q (%v), want %q", got, err, receipt)
	}

	// What each subject's October was delivered, worked by hand.
	for name, gbHours := range map[string]int64{
		"steady":    150, // 350 GB-h on Hobby, 200 included
		"upgrade":   10,  // 210 on Hobby before the upgrade; Pro's share covers the rest
		"downgrade": 0,   // Pro's share of the month covers all 350
		"return":    0,   // the ten days on Pro cover the rest
		"boundary":  60,  // September's 500 GB-h are September's
		"capped":    50,  // its 50-cent cap; its unbilled egress reserves none of it
		"late":      188, // 210 above the allowance after subscribing; the $2 default cap less 4 GiB of egress at 3 cents
		"org":       150, // an organization bills like an account
	} {
		got, err := deliveries.DeliveredComputeMBSeconds(ctx, "polar", subjects[name], month, end)
		if err != nil {
			t.Fatal(err)
		}
		if got != gbh(gbHours) {
			t.Errorf("%s compute delivered %d MB-s in October, want %d (%d GB-h)", name, got, gbh(gbHours), gbHours)
		}
	}
	for name, gibs := range map[string]int64{
		"steady":  4, // 14 GiB from the 5th, 10 included
		"upgrade": 0, // overage off
		"org":     3, // the 5 GiB above the allowance while overage was off stay unbilled
		"late":    4, // 8 GiB on Free count toward the 10 included; 6 more on Hobby
		"capped":  0, // overage off
	} {
		got, err := deliveries.DeliveredEgressBytes(ctx, "polar", subjects[name], month, end)
		if err != nil {
			t.Fatal(err)
		}
		if got != gibs*gib {
			t.Errorf("%s egress delivered %d bytes in October, want %d GiB", name, got, gibs)
		}
	}
}
