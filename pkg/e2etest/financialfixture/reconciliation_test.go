package financialfixture_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest/financialfixture"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

const gib int64 = 1 << 30

type replayFixture struct {
	pool            *pgxpool.Pool
	store           *state.PgStore
	account         state.Account
	app             state.App
	instance        state.Instance
	policy          state.FinancialBudget
	start           time.Time
	want            map[string]int64
	fundedMBSeconds int64
}

// This fixture restores historical usage and settled funding, without running
// guests or claiming native enforcement. The native lane supplies that evidence.
func seedReplay(t *testing.T) replayFixture {
	t.Helper()
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	a, err := s.CreateAccount(ctx, "reconcile-"+uuid.NewString()+"@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "reconcile-" + a.ID[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + fmt.Sprintf("%064d", 0)})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(ctx, app.ID, dep.ID, string(state.StateParked), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-api.MeterUsageFinalAfter - 5*time.Hour).Truncate(24 * time.Hour)
	month := financial.ComputeMonth(start)
	// Match meterd's startup snapshot while preserving four then five paid
	// GB-hours above the plan's included allowance in the replay below.
	computeAllowance := int64(a.Plan.PlanIncludedGBHours()) * api.SecondsPerGBHour
	for _, price := range []financial.Price{
		{Version: "acceptance-compute", Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: computeAllowance},
		{Version: "acceptance-egress", Meter: "egress", Currency: "USD", Unit: "interface_bytes", UnitQuantity: gib, MillicentsPerUnit: 2000, IncludedQuantity: gib},
	} {
		if _, err := s.PutFinancialPriceSnapshot(ctx, state.FinancialPriceSnapshot{AccountID: a.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), EffectiveFrom: month, Plan: a.Plan, DeliveryMode: "live", Price: price}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: a.ID, Provider: "polar", CustomerID: "acceptance-customer", SubscriptionID: "acceptance-subscription", BillingFrom: month}); err != nil {
		t.Fatal(err)
	}
	want := make(map[string]int64)
	// Deliberately insert completed hours out of chronological order.
	// Provider retries below replay the same durable windows in both meters.
	for _, i := range []int{2, 0, 1} {
		mb := api.SecondsPerGBHour
		if i == 0 {
			mb = computeAllowance + 2*api.SecondsPerGBHour
		}
		at := start.Add(time.Duration(i) * time.Hour)
		if err := s.AppendUsage(ctx, a.ID, app.ID, ins.ID, at, mb, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		netMB := api.SecondsPerGBHour
		if i == 0 {
			netMB = 2 * api.SecondsPerGBHour
		}
		want[fmt.Sprintf("faas-usage-%s-%s", a.ID, at.Format(time.RFC3339))] = netMB
		want[fmt.Sprintf("faas-egress-%s-%s", a.ID, at.Format(time.RFC3339))] = gib
	}
	// Cumulative observations are durable across new store handles. Replaying
	// a committed observation must contribute zero additional interface bytes.
	for i := range 3 {
		at := start.Add(time.Duration(i) * time.Hour)
		for replay := range 2 {
			delta, _, err := state.NewPgStore(pool).AppendNetworkUsageObservation(ctx, a.ID, app.ID, ins.ID, at, int64(2+i)*gib, true, 0, false)
			wantDelta := gib
			if i == 0 {
				wantDelta = 2 * gib
			}
			if replay == 1 {
				wantDelta = 0
			}
			if err != nil || delta != wantDelta {
				t.Fatalf("counter replay: delta=%d want=%d err=%v", delta, wantDelta, err)
			}
		}
	}

	// A retained settlement can be more conservative than measured usage:
	// 5 GiB issued, 4 GiB measured, 1 GiB included, 3 GiB actually billed.
	book := financial.ComputeBook{AccountID: a.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), Committed: 5000, QuantityCommitted: computeAllowance + 5*api.SecondsPerGBHour, AllowanceGranted: computeAllowance, Egress: financial.EgressBook{Initialized: true, QuantityCommitted: 5 * gib, AllowanceGranted: gib, Committed: 8000, RateMillicents: 2000, RateQuantity: gib}}
	payload, err := json.Marshal(book)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO financial_compute_account_books(account_id,period_start,period_end,payload) VALUES($1,$2,$3,$4)`, a.ID, month, month.AddDate(0, 1, 0), payload); err != nil {
		t.Fatal(err)
	}
	spec := financial.BudgetSpec{Name: "restored acceptance policy", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute", "egress"}, Basis: "net_usage", LimitMillicents: 20000, NotifyMillicents: []int64{}, Mode: "strict", Action: "suspend_workloads", ResumeRule: "manual", Enabled: true}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	p := state.FinancialBudget{ID: uuid.NewString(), AccountID: a.ID, Revision: 1, Spec: spec}
	if _, err = pool.Exec(ctx, `INSERT INTO financial_budget_policies(id,account_id,revision,spec) VALUES($1,$2,1,$3)`, p.ID, a.ID, data); err != nil {
		t.Fatal(err)
	}
	for at := time.Now().UTC().Truncate(time.Minute).Add(-api.MeterUsageFinalAfter - 5*time.Minute); !at.After(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
		if err := s.RecordFinancialSamplingWindow(ctx, at, true, true); err != nil {
			t.Fatal(err)
		}
	}
	return replayFixture{pool: pool, store: s, account: a, policy: p, start: start, want: want, app: app, instance: ins, fundedMBSeconds: book.QuantityCommitted}
}

func receipts(t *testing.T, f replayFixture) map[string]int64 {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), `SELECT 'faas-usage-'||account_id::text||'-'||to_char(window_start AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),mb_seconds FROM billing_usage_deliveries WHERE provider='polar' AND account_id=$1 UNION ALL SELECT 'faas-egress-'||account_id::text||'-'||to_char(window_start AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),quantity FROM billing_meter_usage_deliveries WHERE provider='polar' AND meter='egress' AND account_id=$1`, f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := make(map[string]int64)
	for rows.Next() {
		var id string
		var q int64
		if err := rows.Scan(&id, &q); err != nil {
			t.Fatal(err)
		}
		if q > 0 {
			got[id] = q
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

func accepted(t *testing.T, p *financialfixture.Provider) map[string]int64 {
	t.Helper()
	s := p.Snapshot()
	if len(s.Conflicts) > 0 {
		t.Fatalf("changed immutable provider event: %v", s.Conflicts)
	}
	got := make(map[string]int64)
	for id, e := range s.Events {
		q, err := e.Quantity()
		if err != nil {
			t.Fatal(err)
		}
		got[id] = q
	}
	return got
}

func TestFinancialSystemBillingReconciliation(t *testing.T) {
	// Probe before compiling the daemon on developer machines without PG.
	pgtest.Open(t)
	binary := financialfixture.BuildMeterd(t)
	for _, scenario := range []string{"provider_unavailable", "accepted_reply_lost", "local_receipt_lost", "restart_after_delivery"} {
		t.Run(scenario, func(t *testing.T) {
			f := seedReplay(t)
			p := financialfixture.NewProvider(t, 2000)
			if scenario == "provider_unavailable" {
				p.FailBeforeAccept()
			}
			if scenario == "accepted_reply_lost" {
				p.LoseAcceptedReplies()
			}
			if scenario == "local_receipt_lost" {
				_, err := f.pool.Exec(t.Context(), `CREATE FUNCTION acceptance_hold_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected local receipt outage'; END $$; CREATE TRIGGER acceptance_hold_compute BEFORE INSERT ON billing_usage_deliveries FOR EACH ROW EXECUTE FUNCTION acceptance_hold_receipt(); CREATE TRIGGER acceptance_hold_egress BEFORE INSERT ON billing_meter_usage_deliveries FOR EACH ROW EXECUTE FUNCTION acceptance_hold_receipt()`)
				if err != nil {
					t.Fatal(err)
				}
			}
			m := financialfixture.StartMeterd(t, binary, f.pool, p.Env(financial.ComputeMonth(f.start)))
			if scenario == "provider_unavailable" {
				financialfixture.Wait(t, m, "provider attempts before acceptance", func() bool { return len(p.Snapshot().Attempts) == len(f.want) })
				if len(accepted(t, p)) != 0 || len(receipts(t, f)) != 0 {
					t.Fatal("unaccepted usage got provider events or local receipts")
				}
			} else {
				financialfixture.Wait(t, m, "provider accepted all immutable windows", func() bool { return reflect.DeepEqual(accepted(t, p), f.want) })
				if scenario != "restart_after_delivery" && len(receipts(t, f)) != 0 {
					t.Fatal("uncertain response or failed transaction got a local receipt")
				}
			}
			if scenario == "restart_after_delivery" {
				financialfixture.Wait(t, m, "original durable delivery completes", func() bool { return reflect.DeepEqual(receipts(t, f), f.want) })
			}
			m.Kill() // actual process loss, with the provider and PostgreSQL retained
			if scenario == "local_receipt_lost" {
				if _, err := f.pool.Exec(t.Context(), `DROP TRIGGER acceptance_hold_compute ON billing_usage_deliveries; DROP TRIGGER acceptance_hold_egress ON billing_meter_usage_deliveries`); err != nil {
					t.Fatal(err)
				}
			}
			p.Recover()
			m.Restart()
			financialfixture.Wait(t, m, "durable receipts reconcile after restart", func() bool { return reflect.DeepEqual(receipts(t, f), f.want) })
			if got := accepted(t, p); !reflect.DeepEqual(got, f.want) {
				t.Fatalf("provider quantities=%v want=%v", got, f.want)
			}
			if scenario == "accepted_reply_lost" {
				for id, n := range p.Snapshot().Attempts {
					if n < 2 {
						t.Fatalf("lost reply %s never replayed", id)
					}
				}
			}
			// Verify the financial ledger independently of funding's unused
			// credit commitment. Actual invoice quantities cost 10,000 mC.
			report, err := billing.ReadFinancialCosts(t.Context(), f.store, f.account.ID, financial.ComputeMonth(f.start), time.Now().UTC())
			if err != nil || report.KnownUsageMillicents != 10000 {
				t.Fatalf("measured ledger=%+v err=%v", report, err)
			}
			b, err := f.store.FinancialComputeBalance(t.Context(), f.policy, f.start)
			if err != nil || b.CommittedMillicents != 13000 || b.ReservedMillicents != 0 || b.Egress == nil || b.Egress.CommittedBytes != 5*gib {
				t.Fatalf("retained funding=%+v err=%v", b, err)
			}
			if report.KnownUsageMillicents > b.CommittedMillicents || b.CommittedMillicents > f.policy.Spec.LimitMillicents {
				t.Fatal("measured invoice escaped shared paid ceiling")
			}
			// A newly finalized window proves the replacement actually scans.
			// Earlier receipts must suppress every extra old wire call.
			before := p.Snapshot().Attempts
			m.Restart()
			at := f.start.Add(3 * time.Hour)
			if err := f.store.AppendUsage(t.Context(), f.account.ID, f.app.ID, f.instance.ID, at, api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
				t.Fatal(err)
			}
			if delta, _, err := f.store.AppendNetworkUsageObservation(t.Context(), f.account.ID, f.app.ID, f.instance.ID, at, 5*gib, true, 0, false); err != nil || delta != gib {
				t.Fatalf("late counter: %d %v", delta, err)
			}
			f.want[fmt.Sprintf("faas-usage-%s-%s", f.account.ID, at.Format(time.RFC3339))] = api.SecondsPerGBHour
			f.want[fmt.Sprintf("faas-egress-%s-%s", f.account.ID, at.Format(time.RFC3339))] = gib
			financialfixture.Wait(t, m, "replacement delivers newly finalized window", func() bool { return reflect.DeepEqual(receipts(t, f), f.want) })
			for id, n := range before {
				if p.Snapshot().Attempts[id] != n {
					t.Fatalf("completed window redelivered: %s", id)
				}
			}
			if !reflect.DeepEqual(accepted(t, p), f.want) {
				t.Fatal("late provider and receipt vectors diverged")
			}
			report, err = billing.ReadFinancialCosts(t.Context(), f.store, f.account.ID, financial.ComputeMonth(f.start), time.Now().UTC())
			if err != nil || report.KnownUsageMillicents != 13000 {
				t.Fatalf("final measured invoice=%d err=%v", report.KnownUsageMillicents, err)
			}
			var measuredMB, measuredBytes int64
			if err := f.pool.QueryRow(t.Context(), `SELECT COALESCE(sum(mb_seconds),0),COALESCE(sum(net_tx_bytes),0) FROM usage_minutes WHERE account_id=$1`, f.account.ID).Scan(&measuredMB, &measuredBytes); err != nil {
				t.Fatal(err)
			}
			if measuredMB != f.fundedMBSeconds || measuredBytes != b.Egress.CommittedBytes {
				t.Fatalf("measured quantities exceed or miss restored funding: mb_seconds=%d bytes=%d", measuredMB, measuredBytes)
			}

			evidence := map[string]any{"scenario": scenario, "account_id": f.account.ID, "measured_mb_seconds": measuredMB, "measured_egress_bytes": measuredBytes, "funded_mb_seconds": f.fundedMBSeconds, "funded_egress_bytes": b.Egress.CommittedBytes, "expected": f.want, "receipts": receipts(t, f), "provider": p.Snapshot(), "initial_invoice_millicents": 10000, "invoice_millicents": report.KnownUsageMillicents, "committed_millicents": b.CommittedMillicents, "limit_millicents": f.policy.Spec.LimitMillicents}
			encoded, err := json.MarshalIndent(evidence, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if dir := os.Getenv("FAAS_FINANCIAL_EVIDENCE_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "reconciliation-"+scenario+".json"), append(encoded, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("reconciled account=%s events=8 initial_invoice_millicents=10000 final_invoice_millicents=13000 committed_millicents=13000 scenario=%s", f.account.ID, scenario)

		})
	}
}

func TestFinancialSystemProviderContractGuard(t *testing.T) {
	pgtest.Open(t)
	binary := financialfixture.BuildMeterd(t)
	f := seedReplay(t)
	p := financialfixture.NewProvider(t, 4000) // catalog agrees, retained funding does not
	m := financialfixture.StartMeterd(t, binary, f.pool, p.Env(financial.ComputeMonth(f.start)))
	financialfixture.Wait(t, m, "funded provider rate refusal", func() bool { return strings.Contains(m.Logs(), "provider egress rate exceeds funded period ceiling") })
	for _, e := range p.Snapshot().Events {
		if e.Name == "faas_egress_usage" {
			t.Fatal("incompatible egress rate reached provider")
		}
	}
	for id := range receipts(t, f) {
		if strings.Contains(id, "faas-egress-") {
			t.Fatal("incompatible rate acknowledged locally")
		}
	}
	m.Stop()
	p2 := financialfixture.NewProvider(t, 2000)
	m2 := financialfixture.StartMeterd(t, binary, f.pool, p2.Env(financial.ComputeMonth(f.start)))
	financialfixture.Wait(t, m2, "compatible replacement delivers held egress", func() bool { return reflect.DeepEqual(receipts(t, f), f.want) })
	if len(p2.Snapshot().Events) != 3 {
		t.Fatalf("replacement accepted %d events; want three held egress windows", len(p2.Snapshot().Events))
	}
}
