package financialtest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type computeTestStore interface {
	state.Store
	state.FinancialStore
	state.FinancialBudgetStore
	state.FinancialComputeStore
	state.ComputeNodeRuntimeIdentityStore
}

// adr: 566 — the real stores must serialize cross-node spending, preserve
// reservations through failed/replayed settlement, and fence old owners.
func TestFinancialComputeReservations(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store computeTestStore = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			computeReservationSuite(t, store)
		})
	}
}

func TestFinancialComputePolicyEditAtUTCReset(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	a := financialAccount(t, store)
	app, err := store.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "compute-reset-edit", RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now().UTC()
	store.SetClockForTest(func() time.Time { return clock })
	nodes, err := store.ListComputeNodes(ctx, false)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("nodes: %v %v", nodes, err)
	}
	var owner runtimeadmission.Identity
	for _, n := range nodes {
		owner = runtimeadmission.Identity{NodeID: n.ID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
		if err := store.RegisterComputeNodeRuntimeIdentity(ctx, owner); err != nil {
			t.Fatal(err)
		}
		if err := store.RegisterFinancialComputeOwner(ctx, owner.NodeID, owner.Incarnation); err != nil {
			t.Fatal(err)
		}
	}
	for at := app.CreatedAt.Truncate(time.Minute); at.Before(clock.Truncate(time.Minute)); at = at.Add(time.Minute) {
		if err := store.RecordFinancialSamplingWindow(ctx, at, true, false); err != nil {
			t.Fatal(err)
		}
	}
	spec := financialBudgetSpec()
	spec.Scope = financial.BudgetScope{Kind: "app", ID: app.ID}
	spec.Mode, spec.Action, spec.Basis = "strict", "suspend_workloads", "gross_usage"
	spec.NotifyMillicents = []int64{}
	spec.DrainSeconds, spec.LimitMillicents, spec.ResumeRule = 0, 1000, "manual"
	p, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "test", spec)
	if err != nil {
		t.Fatal(err)
	}
	request := financial.ComputeRequest{AttemptID: uuid.NewString(), Fenced: true, AccountID: a.ID, AppID: app.ID, InstanceID: uuid.NewString(), NodeID: owner.NodeID, Incarnation: owner.Incarnation, BillableMB: 136}
	lease, err := store.AcquireFinancialCompute(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	spec.LimitMillicents = lease.Reserved
	p, err = store.UpdateFinancialBudget(ctx, a.ID, p.ID, p.Revision, "test", spec)
	if err != nil {
		t.Fatal(err)
	}
	request.AttemptID, request.InstanceID = uuid.NewString(), uuid.NewString()
	if _, err := store.AcquireFinancialCompute(ctx, request); !errors.Is(err, financial.ErrComputeBudget) {
		t.Fatalf("second grant escaped hold: %v", err)
	}
	previous := clock
	clock = financial.ComputeMonth(clock).AddDate(0, 1, 0)
	balance, err := store.FinancialComputeBalance(ctx, p, clock)
	if err != nil || balance.Initialized || !balance.Held || balance.UnconfirmedLeases != 1 {
		t.Fatalf("reset lost manual hold or prior attempt: %+v %v", balance, err)
	}
	spec.LimitMillicents = 1000
	p, err = store.UpdateFinancialBudget(ctx, a.ID, p.ID, p.Revision, "test", spec)
	if err != nil {
		t.Fatalf("cannot edit an existing policy before period initialization: %v", err)
	}
	balance, err = store.FinancialComputeBalance(ctx, p, clock)
	if err != nil || balance.Initialized || balance.Held || balance.UnconfirmedLeases != 1 {
		t.Fatalf("edit minted funding or retained its old hold: %+v %v", balance, err)
	}
	old, err := store.FinancialComputeBalance(ctx, p, previous)
	if err != nil || old.ReservedMillicents != lease.Reserved {
		t.Fatalf("edit released prior physical funding: %+v %v", old, err)
	}
	request.AttemptID = uuid.NewString()
	if _, err := store.AcquireFinancialCompute(ctx, request); !errors.Is(err, financial.ErrComputeUnavailable) {
		t.Fatalf("edit bypassed period readiness: %v", err)
	}
}

func computeReservationSuite(t *testing.T, store computeTestStore) {
	t.Helper()
	ctx := t.Context()
	a := financialAccount(t, store)
	app, err := store.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "compute-reservations", RAMMB: 256, MaxConcurrency: 20})
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := store.ListComputeNodes(ctx, false)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("nodes: %v %v", nodes, err)
	}
	second, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: uuid.NewString(), TargetURL: "unix:///tmp/compute-budget-test.sock", VPCPUs: 8, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 7000, VCPUBudget: 8, Active: true, Lifecycle: state.NodeLifecycleActive})
	if err != nil {
		t.Fatal(err)
	}
	nodes = append(nodes, second)
	identities := []runtimeadmission.Identity{}
	for _, n := range nodes {
		i := runtimeadmission.Identity{NodeID: n.ID, Incarnation: uuid.NewString(), ProtocolVersion: 1}
		if err := store.RegisterComputeNodeRuntimeIdentity(ctx, i); err != nil {
			t.Fatal(err)
		}
		if err := store.RegisterFinancialComputeOwner(ctx, i.NodeID, i.Incarnation); err != nil {
			t.Fatal(err)
		}
		identities = append(identities, i)
	}
	// The first closed minute after creating the app can arrive while CI is
	// compiling. Record it rather than depending on a wall-clock minute race.
	for at := app.CreatedAt.Truncate(time.Minute); at.Before(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
		if err := store.RecordFinancialSamplingWindow(ctx, at, true, false); err != nil {
			t.Fatal(err)
		}
	}
	// Derive the exact funded amount from integral seconds, not two Now calls.
	start := time.Now().UTC().Truncate(time.Second)
	charge := int64(0)
	for offset := range 60 {
		at := start.Truncate(time.Minute).Add(time.Duration(offset) * time.Second)
		candidate, err := financial.ComputeCharge(264, at, at.Add(api.FinancialComputeLeaseDuration+api.FinancialComputeTeardownReserve), api.OverageMillicentsPerGBHour, api.SecondsPerGBHour)
		if err != nil {
			t.Fatal(err)
		}
		charge = max(charge, candidate)
	}
	spec := financialBudgetSpec()
	spec.Scope = financial.BudgetScope{Kind: "app", ID: strings.ToUpper(app.ID)}
	spec.Mode = "strict"
	spec.Action = "suspend_workloads"
	spec.Basis = "gross_usage"
	spec.NotifyMillicents = []int64{}
	spec.LimitMillicents = charge
	spec.ResumeRule = "manual"
	p, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "test", spec)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if p.Spec.Scope.ID != app.ID {
		t.Fatal("scope identity was not canonicalized")
	}
	spec = p.Spec
	request := func(i int) financial.ComputeRequest {
		owner := identities[i%len(identities)]
		return financial.ComputeRequest{AttemptID: uuid.NewString(), Fenced: true, LeaseID: uuid.NewString(), Fence: uuid.NewString(), RequestID: uuid.NewString(), AccountID: a.ID, AppID: app.ID, InstanceID: uuid.NewString(), NodeID: owner.NodeID, Incarnation: owner.Incarnation, BillableMB: 264}
	}
	var mu sync.Mutex
	winners := []financial.ComputeLease{}
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			l, err := store.AcquireFinancialCompute(ctx, request(i))
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				winners = append(winners, l)
			} else if !errors.Is(err, financial.ErrComputeBudget) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(winners) != 1 {
		t.Fatalf("cross-node oversubscription: %d admitted", len(winners))
	}
	l := winners[0]
	b, err := store.FinancialComputeBalance(ctx, p, time.Now())
	if err != nil || b.CommittedMillicents+b.ReservedMillicents > spec.LimitMillicents || b.ReservedMillicents != l.Reserved || !b.Held {
		t.Fatalf("atomic balance: %+v %v", b, err)
	}
	if err := state.CheckFinancialBudgetAdmission(ctx, store, app, "", time.Now()); !errors.Is(err, state.ErrFinancialBudgetHeld) {
		t.Fatalf("warm traffic escaped: %v", err)
	}
	// Expiry alone neither releases money nor bills unfunded stale residency.
	future := l.FundedUntil.Add(time.Minute)
	futureBalance, err := store.FinancialComputeBalance(ctx, p, future)
	if err != nil || futureBalance.ReservedMillicents != l.Reserved {
		t.Fatalf("expiry reclaimed funds: %+v %v", futureBalance, err)
	}
	seconds, err := store.FinancialComputeBillingSeconds(ctx, a.ID, app.ID, l.InstanceID, l.FundedUntil.Truncate(time.Minute).Add(time.Minute), 60)
	if err != nil || seconds != 0 {
		t.Fatalf("stale instance overbilled: %d %v", seconds, err)
	}
	seconds, err = store.FinancialComputeBillingSeconds(ctx, a.ID, app.ID, uuid.NewString(), l.StartedAt.Truncate(time.Minute), 60)
	if err != nil || seconds != 0 {
		t.Fatalf("unfunded instance overbilled: %d %v", seconds, err)
	}
	mutation := financial.ComputeRequest{LeaseID: l.ID, Fence: l.Fence, RequestID: uuid.NewString(), AccountID: l.AccountID, AppID: l.AppID, InstanceID: l.InstanceID, NodeID: l.NodeID, Incarnation: l.Incarnation, BillableMB: l.BillableMB, Revision: l.Revision}
	wrong := mutation
	wrong.Fence = uuid.NewString()
	if _, err := store.SettleFinancialCompute(ctx, wrong); !errors.Is(err, financial.ErrComputeFence) {
		t.Fatalf("foreign fence settled money: %v", err)
	}
	other := financialAccount(t, store)
	wrong = mutation
	wrong.AccountID = other.ID
	if _, err := store.SettleFinancialCompute(ctx, wrong); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign account settled money: %v", err)
	}
	// An API retry cannot lower the budget below already issued funding.
	lower := spec
	lower.LimitMillicents = 0
	if _, err := store.UpdateFinancialBudget(ctx, a.ID, p.ID, p.Revision, "test", lower); !errors.Is(err, financial.ErrComputeBudget) {
		t.Fatalf("lowered below reservation: %v", err)
	}
	// Settlement is an internal physical-owner acknowledgement. No timeout
	// method exists; before this call every millicent remains reserved.
	settled, err := store.SettleFinancialCompute(ctx, mutation)
	if err != nil || settled.SettledAt == nil || settled.Reserved != 0 {
		t.Fatalf("settle: %+v %v", settled, err)
	}
	replay, err := store.SettleFinancialCompute(ctx, mutation)
	if err != nil || replay.Revision != settled.Revision || replay.Committed != settled.Committed {
		t.Fatalf("settlement replay: %+v %v", replay, err)
	}
	// Recovery refreshes an exact, account-scoped receipt. A later attempt on
	// the same instance must never substitute for this settled generation.
	read, found, err := store.FinancialComputeLease(ctx, a.ID, settled.ID)
	if err != nil || !found || read.Revision != settled.Revision || read.SettledAt == nil {
		t.Fatalf("exact receipt read: %+v %v %v", read, found, err)
	}
	*read.SettledAt = time.Time{}
	read, found, err = store.FinancialComputeLease(ctx, a.ID, settled.ID)
	if err != nil || !found || read.SettledAt == nil || read.SettledAt.IsZero() {
		t.Fatalf("receipt read mutated authority: %+v %v %v", read, found, err)
	}
	if _, found, err := store.FinancialComputeLease(ctx, other.ID, settled.ID); err != nil || found {
		t.Fatalf("foreign account read funding receipt: %v %v", found, err)
	}
	if _, found, err := store.FinancialComputeLease(ctx, a.ID, uuid.NewString()); err != nil || found {
		t.Fatalf("missing receipt became authority: %v %v", found, err)
	}
	cancelled, cancelRead := context.WithCancel(ctx)
	cancelRead()
	if _, _, err := store.FinancialComputeLease(cancelled, a.ID, settled.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled receipt read: %v", err)
	}
	b, err = store.FinancialComputeBalance(ctx, p, time.Now())
	if err != nil || b.ReservedMillicents != 0 || b.CommittedMillicents == 0 || !b.Held {
		t.Fatalf("settlement erased spending/hold: %+v %v", b, err)
	}
	// A manual hold crosses the UTC boundary; the new month's book is empty,
	// but confirmed old funding and retained intent are not reset together.
	next, err := store.FinancialComputeBalance(ctx, p, financial.ComputeMonth(time.Now()).AddDate(0, 1, 0))
	if err != nil || !next.Held {
		t.Fatalf("manual UTC hold: %+v %v", next, err)
	}
	spec.LimitMillicents = 1000
	p, err = store.UpdateFinancialBudget(ctx, a.ID, p.ID, p.Revision, "test", spec)
	if err != nil {
		t.Fatal(err)
	}
	b, err = store.FinancialComputeBalance(ctx, p, time.Now())
	if err != nil || b.Held || b.CommittedMillicents != settled.Committed {
		t.Fatalf("revision reset spending: %+v %v", b, err)
	}
	r := request(0)
	live, err := store.AcquireFinancialCompute(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := store.AcquireFinancialCompute(ctx, r)
	if err != nil || replayed.ID != live.ID || replayed.Reserved != live.Reserved {
		t.Fatalf("acquire replay: %+v %v", replayed, err)
	}
	owner := identities[0]
	newer := owner
	newer.Incarnation = uuid.NewString()
	if err := store.RegisterComputeNodeRuntimeIdentity(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterFinancialComputeOwner(ctx, newer.NodeID, newer.Incarnation); err != nil {
		t.Fatal(err)
	}
	mutation = financial.ComputeRequest{LeaseID: live.ID, Fence: live.Fence, RequestID: uuid.NewString(), Revision: live.Revision, AccountID: a.ID, AppID: app.ID, InstanceID: live.InstanceID, NodeID: live.NodeID, Incarnation: live.Incarnation}
	if _, err := store.RenewFinancialCompute(ctx, mutation); !errors.Is(err, financial.ErrComputeFence) {
		t.Fatalf("superseded vmmd renewed: %v", err)
	}
	if _, err := store.SettleFinancialCompute(ctx, mutation); err != nil {
		t.Fatalf("recovery cannot settle exact old incarnation: %v", err)
	}
	identities[0] = newer
	// Every matching policy must fit. Deleting the more restrictive policy
	// releases only its own hold and preserves the shared app/month spending.
	before, err := store.FinancialComputeBalance(ctx, p, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	secondSpec := spec
	secondSpec.Name = "Second strict cap"
	secondSpec.LimitMillicents = before.CommittedMillicents
	secondPolicy, err := store.CreateFinancialBudget(ctx, a.ID, uuid.NewString(), "test", secondSpec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireFinancialCompute(ctx, request(1)); !errors.Is(err, financial.ErrComputeBudget) {
		t.Fatalf("matching policy bypassed: %v", err)
	}
	firstBalance, err := store.FinancialComputeBalance(ctx, p, time.Now())
	if err != nil || firstBalance.Held || firstBalance.ReservedMillicents != 0 {
		t.Fatalf("unrelated policy hold: %+v %v", firstBalance, err)
	}
	secondBalance, err := store.FinancialComputeBalance(ctx, secondPolicy, time.Now())
	if err != nil || !secondBalance.Held {
		t.Fatalf("restrictive policy not held: %+v %v", secondBalance, err)
	}
	if _, err := store.DeleteFinancialBudget(ctx, a.ID, secondPolicy.ID, secondPolicy.Revision, "test"); err != nil {
		t.Fatal(err)
	}
	final, err := store.AcquireFinancialCompute(ctx, request(1))
	if err != nil {
		t.Fatalf("own hold not released: %v", err)
	}
	month := financial.ComputeMonth(final.StartedAt)
	_, err = store.PutFinancialPriceSnapshot(ctx, state.FinancialPriceSnapshot{AccountID: a.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), Plan: api.PlanHobby, EffectiveFrom: final.StartedAt.Truncate(time.Minute), DeliveryMode: "live", Price: financial.Price{Version: "strict-higher-price", Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: 2 * api.OverageMillicentsPerGBHour}})
	if err != nil {
		t.Fatal(err)
	}
	seconds, err = store.FinancialComputeBillingSeconds(ctx, a.ID, app.ID, final.InstanceID, final.StartedAt.Truncate(time.Minute), 60)
	if err != nil || seconds != 0 {
		t.Fatalf("unfunded price increase billed: %d %v", seconds, err)
	}
	wrongCancel := financial.ComputeRequest{AppID: app.ID, InstanceID: final.InstanceID, AttemptID: final.AttemptID, NodeID: newer.NodeID, Incarnation: newer.Incarnation, Fenced: true}
	if _, err := store.CancelFinancialCompute(ctx, wrongCancel); !errors.Is(err, financial.ErrComputeFence) {
		t.Fatalf("foreign node cancelled funding: %v", err)
	}
	wrongCancel.NodeID = final.NodeID
	wrongCancel.Incarnation = final.Incarnation
	closed, err := store.CancelFinancialCompute(ctx, wrongCancel)
	if err != nil || closed.SettledAt == nil || closed.Reserved != 0 {
		t.Fatalf("unstarted grant cancellation: %+v %v", closed, err)
	}
	late := financial.ComputeRequest{AccountID: a.ID, AppID: app.ID, InstanceID: final.InstanceID, AttemptID: final.AttemptID, NodeID: final.NodeID, Incarnation: final.Incarnation, BillableMB: 264, Fenced: true}
	if _, err := store.AcquireFinancialCompute(ctx, late); !errors.Is(err, financial.ErrComputeFence) {
		t.Fatalf("settled attempt replayed as a fresh VM: %v", err)
	}
	late.InstanceID = uuid.NewString()
	late.AttemptID = uuid.NewString()
	if _, err := store.CancelFinancialCompute(ctx, late); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireFinancialCompute(ctx, late); !errors.Is(err, financial.ErrComputeFence) {
		t.Fatalf("late RPC escaped cancellation: %v", err)
	}
	retired, err := store.FinancialComputeRetired(ctx, late.NodeID, late.InstanceID)
	if err != nil || !retired {
		t.Fatalf("missing retirement receipt: %v %v", retired, err)
	}
	if read, found, err := store.FinancialComputeLease(ctx, a.ID, settled.ID); err != nil || !found || read.ID != settled.ID || read.SettledAt == nil {
		t.Fatalf("later attempts hid original receipt: %+v %v %v", read, found, err)
	}
}
