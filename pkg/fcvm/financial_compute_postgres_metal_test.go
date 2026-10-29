//go:build linux && metal

// adr: 566 — qualify real PostgreSQL funding against native process ownership.
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// Both nodes are independent OS processes on one isolated host. Only one may
// reach native allocation: this is not a physical two-host/network-partition
// qualification. No scheduler, gateway or meter process keeps these VMs alive.
func TestMetalFinancialComputePostgresContention(t *testing.T) {
	f := newFinancialNativeFixture(t)
	owners := []*financialNativeOwner{f.start(0, ""), f.start(1, "")}
	f.activate()
	var results [2]financialNativeEvent
	var wg sync.WaitGroup
	for i, owner := range owners {
		wg.Go(func() { results[i] = owner.call(t, financialNativeCommand{Operation: "wake"}) })
	}
	wg.Wait()
	winner := -1
	for i, result := range results {
		if result.Error == "" {
			if winner != -1 || result.Live != 1 || result.Leased != 1 {
				t.Fatalf("more than one native start or no real guest: %+v", results)
			}
			winner = i
		} else if result.Error != "budget" || result.Live != 0 || result.Leased != 0 || result.Journal {
			t.Fatalf("losing node performed physical work: %+v", result)
		}
	}
	if winner == -1 {
		t.Fatalf("neither native owner booted: %+v", results)
	}
	lease := f.lease(owners[winner])
	if !lease.Strict || lease.Reserved <= 0 {
		t.Fatalf("native execution has no real funding: %+v", lease)
	}
	f.assertBalance(lease.Reserved, lease.Committed)
	// A rejected attempt remains rejected after the other VM retires and the
	// operator releases the manual hold. Reusing a wake ID cannot spend again.
	owners[winner].must(t, "destroy")
	f.assertSettled(owners[winner], lease)
	f.releaseHold()
	loser := owners[1-winner]
	replay := loser.call(t, financialNativeCommand{Operation: "wake"})
	if replay.Error != "budget" || replay.Leased != 0 || replay.Journal {
		t.Fatalf("denied wake replay minted native authority: %+v", replay)
	}
}

// The shared allowance must authorize only one real physical attempt even
// when separate native owners request different apps on the same account.
func TestMetalFinancialComputePostgresAccountContention(t *testing.T) {
	f := newFinancialNativeFixture(t)
	app, err := f.store.CreateApp(f.ctx, state.App{AccountID: f.app.AccountID, Slug: "native-account-second", RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	f.configs[1].AppID = app.ID
	owners := []*financialNativeOwner{f.start(0, ""), f.start(1, "")}
	account, err := f.store.AccountByID(f.ctx, f.app.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	for at := account.CreatedAt.Truncate(time.Minute); at.Before(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
		if err := f.store.RecordFinancialSamplingWindow(f.ctx, at, true, false); err != nil {
			t.Fatal(err)
		}
	}
	month := financial.ComputeMonth(time.Now())
	grant := int64(api.BillableRAMMB(128)) * int64((api.FinancialComputeLeaseDuration+api.FinancialComputeTeardownReserve)/time.Second)
	_, err = f.store.PutFinancialPriceSnapshot(f.ctx, state.FinancialPriceSnapshot{AccountID: f.app.AccountID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), EffectiveFrom: month, Plan: api.PlanHobby, DeliveryMode: "live", Price: financial.Price{Version: "native-account", Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: grant}})
	if err != nil {
		t.Fatal(err)
	}
	spec := financial.BudgetSpec{Name: "native account compute", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: 1, NotifyMillicents: []int64{}, Mode: "strict", Action: "suspend_workloads", ResumeRule: "manual", Enabled: true}
	f.policy, err = f.store.CreateFinancialBudget(f.ctx, f.app.AccountID, uuid.NewString(), "native-acceptance", spec)
	if err != nil {
		t.Fatal(err)
	}
	var results [2]financialNativeEvent
	var wg sync.WaitGroup
	for i, owner := range owners {
		wg.Go(func() { results[i] = owner.call(t, financialNativeCommand{Operation: "wake"}) })
	}
	wg.Wait()
	winner := -1
	for i, result := range results {
		if result.Error == "" {
			if winner != -1 || result.Live != 1 || result.Leased != 1 {
				t.Fatalf("shared allowance overspent: %+v", results)
			}
			winner = i
		} else if result.Error != "budget" || result.Live != 0 || result.Leased != 0 || result.Journal {
			t.Fatalf("unfunded physical effects: %+v", result)
		}
	}
	if winner < 0 {
		t.Fatalf("no funded native owner: %+v", results)
	}
	lease := f.lease(owners[winner])
	balance, err := f.store.FinancialComputeBalance(f.ctx, f.policy, time.Now())
	if err != nil || !lease.AccountFunded || balance.AllowanceReserved != grant || balance.ReservedMillicents != 0 || !balance.Held || balance.UnconfirmedLeases != 1 {
		t.Fatalf("account/native funding mismatch: %+v %+v %v", lease, balance, err)
	}
	owners[winner].must(t, "destroy")
	settled := f.lease(owners[winner])
	balance, err = f.store.FinancialComputeBalance(f.ctx, f.policy, time.Now())
	if err != nil || settled.SettledAt == nil || settled.QuantityReserved != 0 || balance.AllowanceReserved != 0 || balance.UnconfirmedLeases != 0 || !balance.Held {
		t.Fatalf("physical retirement lost account funding: %+v %+v %v", settled, balance, err)
	}
}

func TestMetalFinancialComputePostgresDeadline(t *testing.T) {
	f := newFinancialNativeFixture(t)
	owner := f.start(0, "")
	// Strict activation also requires a current owner for the second active
	// node. It need not boot a VM in this test.
	f.start(1, "")
	f.activate()
	owner.must(t, "wake")
	lease := f.lease(owner)
	if lease.RunUntil.Before(time.Now()) {
		t.Fatal("fixture did not leave time to disconnect PostgreSQL")
	}
	// This closes the owner's real pgx pool, including heartbeat/renew/settle
	// traffic. The observer uses a separate connection to the same database.
	owner.must(t, "offline")
	lease = f.lease(owner) // Join any renewal already in flight at disconnect.
	f.assertBalance(lease.Reserved, lease.Committed)
	deadline := lease.RunUntil.Add(api.FinancialComputeTeardownReserve)
	owner.wait(t, deadline, func(e financialNativeEvent) bool {
		return e.Physical == 0 && e.ResourcesRemoved && e.Leased > 0
	})
	if time.Now().After(lease.FundedUntil.Add(2 * time.Second)) {
		t.Fatal("native retirement exceeded its funded teardown window")
	}
	// Physical absence while the DB is unreachable cannot release money.
	f.assertBalance(lease.Reserved, lease.Committed)
	if current := f.lease(owner); current.SettledAt != nil {
		t.Fatal("database outage silently settled funding")
	}
	owner.kill(t)
	replacement := f.start(0, "")
	if replacement.identity.Incarnation == owner.identity.Incarnation {
		t.Fatal("owner restart did not change incarnation")
	}
	replacement.must(t, "recover")
	f.assertSettled(replacement, lease)
	// The same persisted wake ID must still be fenced after daemon death.
	if replay := replacement.call(t, financialNativeCommand{Operation: "wake"}); replay.Error != "fence" || replay.Journal && !replay.ResourcesRemoved || replay.Leased != 0 {
		t.Fatalf("restarted owner replayed settled execution: %+v", replay)
	}
}

func TestMetalFinancialComputePostgresCrashRecovery(t *testing.T) {
	for _, phase := range []string{"boot", "live", "teardown"} {
		t.Run(phase, func(t *testing.T) {
			f := newFinancialNativeFixture(t)
			owner := f.start(0, phase)
			f.start(1, "")
			f.activate()
			owner.send(t, financialNativeCommand{Operation: "wake"})
			if phase == "boot" {
				if e := owner.receive(t); e.Operation != "checkpoint" {
					t.Fatalf("no native boot checkpoint: %+v", e)
				}
			} else {
				if e := owner.receive(t); e.Error != "" || e.Live != 1 {
					t.Fatalf("native boot: %+v", e)
				}
				if phase == "teardown" {
					owner.send(t, financialNativeCommand{Operation: "destroy"})
					if e := owner.receive(t); e.Operation != "checkpoint" || !e.ResourcesRemoved {
						t.Fatalf("no physical retirement checkpoint: %+v", e)
					}
				}
			}
			lease := f.lease(owner)
			owner.kill(t)
			// Death, elapsed time, and an absent API connection are never
			// sufficient retirement evidence for the money ledger.
			f.assertBalance(lease.Reserved, lease.Committed)
			replacement := f.start(0, "")
			if replacement.identity.Incarnation == owner.identity.Incarnation || replacement.ready.Live != 0 {
				t.Fatal("restart published recovered execution as live")
			}
			if phase != "teardown" && replacement.ready.Leased == 0 {
				t.Fatal("restart failed to quarantine the native attempt")
			}
			// A failed settlement after real physical retirement keeps the
			// allocation and money unavailable, including across restart.
			replacement.must(t, "settlement-off")
			failed := replacement.call(t, financialNativeCommand{Operation: "recover"})
			if failed.Error == "" || failed.Leased == 0 || !failed.ResourcesRemoved {
				t.Fatalf("failed settlement released native ownership: %+v", failed)
			}
			f.assertBalance(lease.Reserved, lease.Committed)
			replacement.must(t, "settlement-on")
			replacement.must(t, "recover")
			f.assertSettled(replacement, lease)
		})
	}
}

// Kill failure is injected at the native pidfd boundary, with a real running
// guest and a real database grant. The fault does not replace process inventory.
func TestMetalFinancialComputePostgresFailedKill(t *testing.T) {
	f := newFinancialNativeFixture(t)
	owner := f.start(0, "")
	f.start(1, "")
	f.activate()
	owner.must(t, "wake")
	lease := f.lease(owner)
	owner.must(t, "kill-off")
	failed := owner.call(t, financialNativeCommand{Operation: "destroy"})
	if failed.Error == "" || failed.Leased == 0 || failed.ResourcesRemoved {
		t.Fatalf("failed native kill released ownership: %+v", failed)
	}
	f.assertBalance(lease.Reserved, lease.Committed)
	owner.kill(t)
	replacement := f.start(0, "")
	replacement.must(t, "recover")
	f.assertSettled(replacement, lease)
}

type financialNativeFixture struct {
	t        *testing.T
	ctx      context.Context
	store    *state.PgStore
	app      state.App
	policy   state.FinancialBudget
	configs  [2]financialNativeConfig
	owners   []*financialNativeOwner
	root     string
	jailRoot string
	proxies  [2]postgresfixture.Proxy
}

func newFinancialNativeFixture(t *testing.T) *financialNativeFixture {
	t.Helper()
	if os.Getenv("FAAS_FINANCIAL_COMPUTE_ACCEPTANCE") != "1" {
		t.Skip("opt-in native PostgreSQL acceptance: FAAS_FINANCIAL_COMPUTE_ACCEPTANCE=1")
	}
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Fatal("financial native acceptance requires root on dedicated x86_64 Linux KVM")
	}
	if info, err := os.Stat("/dev/kvm"); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		t.Fatal("financial native acceptance requires /dev/kvm")
	}
	for _, name := range []string{"FAAS_TEST_KERNEL", "FAAS_TEST_BASE_ROOTFS", "FAAS_TEST_LAYER_ROOTFS", "FAAS_TEST_VMMD_BINARY", "FAAS_FINANCIAL_ACCEPTANCE_ROOT", "DATABASE_URL"} {
		if os.Getenv(name) == "" {
			t.Fatalf("native financial fixture missing %s", name)
		}
	}
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("financial acceptance cannot skip PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	t.Cleanup(cancel)
	// pgtest intentionally skips unavailable databases in ordinary developer
	// runs. Qualification must fail instead, before opening its private schema.
	preflight, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal("invalid acceptance PostgreSQL configuration")
	}
	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	err = preflight.Ping(pingCtx)
	pingCancel()
	preflight.Close()
	if err != nil {
		t.Fatal("acceptance PostgreSQL is unreachable")
	}
	leakcheck.AssertZero(t)
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	a, err := store.CreateAccount(ctx, uuid.NewString()+"@native-cost.example", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "native-cost", RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "cost-second", TargetURL: "unix:///unused-native-cost.sock", VPCPUs: 2, MemMB: 4096, MaxConcurrency: 2, AdmissionCeilingMB: 3000, VCPUBudget: 2, Active: true, Lifecycle: state.NodeLifecycleActive})
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(os.Getenv("FAAS_FINANCIAL_ACCEPTANCE_ROOT"), "cost-")
	if err != nil {
		t.Fatal(err)
	}
	// Keep Firecracker/vsock Unix sockets below sockaddr_un's path limit.
	// Persistent image staging stays in the runner's long, exact-commit tree.
	var jailRoot string
	if os.Getenv("FAAS_FINANCIAL_PAIR") == "1" {
		jailParent := os.Getenv("FAAS_FINANCIAL_PAIR_JAIL_ROOT")
		if jailParent == "" {
			t.Fatal("native pair jail root is missing")
		}
		jailRoot, err = financialPairJailDirectory(jailParent)
	} else {
		jailRoot, err = os.MkdirTemp("/srv/fc", "cost-")
	}
	if err != nil {
		_ = os.Remove(root) // no producer has started
		t.Fatal(err)
	}
	f := &financialNativeFixture{t: t, ctx: ctx, store: store, app: app, root: root, jailRoot: jailRoot}
	for i, node := range []state.ComputeNode{first, second} {
		f.configs[i] = financialNativeConfig{DBURL: os.Getenv("DATABASE_URL"), Database: pool.Config().ConnConfig.Database, RuntimeParams: pool.Config().ConnConfig.RuntimeParams, NodeID: node.ID, AccountID: a.ID, AppID: app.ID, Instance: uuid.NewString(), Attempt: uuid.NewString(), Root: filepath.Join(root, fmt.Sprintf("node-%d", i)), JailRoot: filepath.Join(jailRoot, fmt.Sprintf("n%d", i))}
	}
	f.configurePair(pool)
	t.Cleanup(f.cleanup)
	return f
}

func (f *financialNativeFixture) start(node int, phase string) *financialNativeOwner {
	f.t.Helper()
	cfg := f.configs[node]
	cfg.Phase = phase
	owner := startFinancialNativeOwner(f.t, f.ctx, cfg)
	f.owners = append(f.owners, owner)
	f.recordPairOwner(node, owner)
	return owner
}

func (f *financialNativeFixture) activate() {
	f.t.Helper()
	// All closed minutes since creation must have positive completeness
	// evidence, even when compilation/setup crosses a UTC minute.
	for at := f.app.CreatedAt.Truncate(time.Minute); at.Before(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
		if err := f.store.RecordFinancialSamplingWindow(f.ctx, at, true, false); err != nil {
			f.t.Fatal(err)
		}
	}
	// Fit one maximum rounded grant and less than two minimum grants. This
	// makes the race deterministic on either side of a UTC minute boundary.
	minimum, maximum := int64(1<<62), int64(0)
	for offset := range 60 {
		at := time.Now().UTC().Truncate(time.Minute).Add(time.Duration(offset) * time.Second)
		charge, err := financial.ComputeCharge(api.BillableRAMMB(128), at, at.Add(api.FinancialComputeLeaseDuration+api.FinancialComputeTeardownReserve), api.OverageMillicentsPerGBHour, api.SecondsPerGBHour)
		if err != nil {
			f.t.Fatal(err)
		}
		minimum, maximum = min(minimum, charge), max(maximum, charge)
	}
	if maximum >= 2*minimum {
		f.t.Fatal("fixture cannot fund exactly one rounded grant")
	}
	spec := financial.BudgetSpec{Name: "native compute acceptance", Scope: financial.BudgetScope{Kind: "app", ID: f.app.ID}, Currency: "USD", Meters: []string{"compute"}, Basis: "gross_usage", LimitMillicents: maximum, NotifyMillicents: []int64{}, Mode: "strict", Action: "suspend_workloads", ResumeRule: "manual", Enabled: true}
	var err error
	f.policy, err = f.store.CreateFinancialBudget(f.ctx, f.app.AccountID, uuid.NewString(), "native-acceptance", spec)
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *financialNativeFixture) lease(owner *financialNativeOwner) financial.ComputeLease {
	f.t.Helper()
	key := owner.config.AppID
	if owner.config.JobID != "" {
		key = "job:" + owner.config.JobID
	}
	l, found, err := f.store.FinancialComputeInstanceLease(f.ctx, f.app.AccountID, key, owner.config.Instance)
	if err != nil || !found {
		f.t.Fatalf("native financial lease missing: %v %v", found, err)
	}
	return l
}

func (f *financialNativeFixture) assertBalance(reserved, committed int64) {
	f.t.Helper()
	b, err := f.store.FinancialComputeBalance(f.ctx, f.policy, time.Now())
	if err != nil || !b.Initialized || b.ReservedMillicents != reserved || b.CommittedMillicents != committed || b.ReservedMillicents+b.CommittedMillicents > f.policy.Spec.LimitMillicents {
		f.t.Fatalf("native funding balance: %+v %v; want reserved=%d committed=%d", b, err, reserved, committed)
	}
}

// assertAccountSettled checks a settled lease against the shared account book.
// That book counts mb-seconds against its included allowance (net usage), so
// it commits money only for quantity beyond the allowance, not the lease's
// gross committed funding that an app or job book records.
func (f *financialNativeFixture) assertAccountSettled(final financial.ComputeLease) {
	f.t.Helper()
	b, err := f.store.FinancialComputeBalance(f.ctx, f.policy, time.Now())
	within := final.QuantityCommitted <= b.AllowanceGranted
	if err != nil || !b.Initialized || b.AllowanceReserved != 0 || b.ReservedMillicents != 0 || b.UnconfirmedLeases != 0 ||
		b.AllowanceCommitted != min(b.AllowanceGranted, final.QuantityCommitted) ||
		within && b.CommittedMillicents != 0 || !within && (b.CommittedMillicents <= 0 || b.CommittedMillicents > final.Committed) {
		f.t.Fatalf("native account settlement: lease=%+v balance=%+v %v", final, b, err)
	}
}

func (f *financialNativeFixture) releaseHold() {
	f.t.Helper()
	var err error
	f.policy, err = f.store.UpdateFinancialBudget(f.ctx, f.app.AccountID, f.policy.ID, f.policy.Revision, "native-acceptance", f.policy.Spec)
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *financialNativeFixture) assertSettled(owner *financialNativeOwner, original financial.ComputeLease) {
	f.t.Helper()
	final := f.lease(owner)
	if final.ID != original.ID || final.Fence != original.Fence || final.SettledAt == nil || final.Reserved != 0 || final.Committed <= 0 || final.Committed > original.Reserved+original.Committed {
		f.t.Fatalf("native funding was not settled within its ceiling: %+v", final)
	}
	if original.AccountFunded {
		f.assertAccountSettled(final)
	} else {
		f.assertBalance(0, final.Committed)
	}
	// Recovery is replayable. Neither money nor receipt revision changes.
	owner.must(f.t, "recover")
	if replay := f.lease(owner); replay.Revision != final.Revision || replay.Committed != final.Committed || !replay.SettledAt.Equal(*final.SettledAt) {
		f.t.Fatalf("recovery settled twice: %+v", replay)
	}
	// Reconcile every funded minute through the production billing clipper,
	// including stale residency after expiry and the synthetic minimum waiver.
	billed := int64(0)
	for at := final.StartedAt.Truncate(time.Minute); !at.After(final.FundedUntil); at = at.Add(time.Minute) {
		seconds, err := f.store.FinancialComputeBillingSeconds(f.ctx, f.app.AccountID, f.app.ID, final.InstanceID, at, 60)
		if err != nil || seconds < 0 || seconds > 60 {
			f.t.Fatalf("funded billing window: %d %v", seconds, err)
		}
		cost, err := financial.CostMeter(f.app.AccountID, at, at.Add(time.Minute), financial.Price{
			Version: "native-pinned", Meter: "compute", Currency: "USD", Unit: "mb_seconds",
			UnitQuantity: final.RateQuantity, MillicentsPerUnit: final.RateMillicents,
		}, []financial.Evidence{{AccountID: f.app.AccountID, SourceID: at.String(), Meter: "compute",
			Quantity: int64(final.BillableMB) * seconds, Start: at, End: at.Add(time.Minute),
			Attribution: financial.Attribution{AppID: f.app.ID}}})
		if err != nil {
			f.t.Fatal(err)
		}
		billed += cost.GrossMillicents
	}
	if billed > final.Committed {
		f.t.Fatalf("billed compute %d exceeds committed funding %d", billed, final.Committed)
	}
	after := final.FundedUntil.Truncate(time.Minute).Add(time.Minute)
	seconds, err := f.store.FinancialComputeBillingSeconds(f.ctx, f.app.AccountID, f.app.ID, final.InstanceID, after, 60)
	if err != nil || seconds != 0 {
		f.t.Fatalf("stale residency billed beyond funding: %d %v", seconds, err)
	}
	// Only an app has a synthetic billing floor to waive; a job has none.
	if final.JobID == "" {
		covered, err := f.store.FinancialComputeFloorCovered(f.ctx, f.app.AccountID, f.app.ID, final.PeriodStart)
		if err != nil || !covered {
			f.t.Fatalf("funded app retained synthetic billing floor: %v %v", covered, err)
		}
	}
	status := owner.must(f.t, "status")
	if status.Physical != 0 || status.Leased != 0 || status.Live != 0 || !status.ResourcesRemoved {
		f.t.Fatalf("settlement preceded full native cleanup: %+v", status)
	}
	leakcheck.AssertZero(f.t)
}

func (f *financialNativeFixture) cleanup() {
	if os.Getenv("FAAS_FINANCIAL_PAIR") == "1" {
		for _, proxy := range f.proxies {
			proxy.SetAvailable(true)
		}
	}
	// Stop all producers first, including any blocked checkpoint. Then recover
	// nodes with outstanding money before empty roots, because this is one host.
	for _, owner := range f.owners {
		owner.kill(f.t)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	order := []int{0, 1}
	if rows, err := f.store.FinancialComputeNodeLeases(ctx, f.configs[1].NodeID); err == nil && len(rows) > 0 {
		order = []int{1, 0}
	}
	clean := true
	for _, i := range order {
		// Use a child even for cleanup: process-lifetime locks must be released
		// by actual process death, just as they are in vmmd.
		cfg := f.configs[i]
		owner, err := launchFinancialNativeOwner(ctx, cfg)
		if err != nil {
			f.t.Errorf("native financial cleanup startup: %v", err)
			clean = false
			continue
		}
		if e, err := owner.exchange(ctx, financialNativeCommand{Operation: "recover"}); err != nil || e.Error != "" || e.Physical != 0 || e.Leased != 0 {
			f.t.Errorf("native financial cleanup: %+v %v", e, err)
			clean = false
		}
		owner.kill(f.t)
	}
	if errs := leakcheck.Zero(); len(errs) != 0 {
		f.t.Errorf("native financial cleanup leaks: %v", errs)
		clean = false
	}
	if clean {
		if os.Getenv("FAAS_FINANCIAL_PAIR") == "1" {
			for _, root := range []string{f.root, f.jailRoot} {
				cmd, err := financialPeerCommand(ctx, f.configs[1].PeerTarget, "remove", root)
				if err == nil {
					err = cmd.Run()
				}
				if err != nil {
					f.t.Errorf("native pair remote cleanup: %v", err)
					clean = false
				}
			}
		}
	}
	if clean {
		for _, root := range []string{f.root, f.jailRoot} {
			if err := os.RemoveAll(root); err != nil {
				f.t.Error(err)
			}
		}
	} else {
		f.t.Logf("retained native ownership evidence for repair: images=%s journal=%s", f.root, f.jailRoot)
	}
}

func financialNativeError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, financial.ErrComputeFence):
		return "fence"
	case errors.Is(err, financial.ErrComputeBudget):
		return "budget"
	default:
		return "unavailable"
	}
}
