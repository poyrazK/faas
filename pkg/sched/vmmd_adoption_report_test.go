package sched

// adr: 20261017 vmmd restart adoption

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type reportingVMM struct {
	*fakeVMM
	report  func() (AdoptionReport, error)
	fetches int
}

func (v *reportingVMM) AdoptionReport(_ context.Context, _ string) (AdoptionReport, error) {
	v.fetches++
	return v.report()
}

type adoptionFixture struct {
	store       *state.MemStore
	engine      *Engine
	vmm         *reportingVMM
	rec         *AdoptionReportReconciler
	node        string
	appID       string
	depID       string
	incarnation string
	epoch       uint64
}

func newAdoptionFixture(t *testing.T) *adoptionFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanScale, 128, 20)
	vmm := &reportingVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	f := &adoptionFixture{store: store, engine: engine, vmm: vmm, node: node.ID, appID: app.ID, depID: dep.ID}
	f.rec = NewAdoptionReportReconciler(engine, testLog())
	return f
}

// restart claims the node's next epoch, as a restarted vmmd does.
func (f *adoptionFixture) restart(t *testing.T) {
	t.Helper()
	f.incarnation = uuid.NewString()
	next, err := f.store.ClaimComputeNodeVMMDEpoch(context.Background(), f.node, f.epoch, f.incarnation)
	if err != nil {
		t.Fatal(err)
	}
	f.epoch = next
}

func (f *adoptionFixture) instance(t *testing.T, st state.State) state.Instance {
	t.Helper()
	ins, err := f.store.CreateInstance(context.Background(), f.appID, f.depID, string(state.StateRunning), 128, f.node, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.engine.Ledger().Admit(Request{AppID: f.appID, Instance: ins.ID, NodeID: f.node, RAMMB: 128, Plan: api.PlanScale, MaxConcurrency: 20}); err != nil {
		t.Fatal(err)
	}
	if st != state.StateRunning {
		if err := f.store.UpdateInstanceState(context.Background(), ins.ID, string(st)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.store.InstanceByID(context.Background(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (f *adoptionFixture) report(adopted, leased []string, refused map[string]string) AdoptionReport {
	r := AdoptionReport{NodeID: f.node, Epoch: f.epoch, Incarnation: f.incarnation, Complete: true,
		Adopted: map[string]string{}, Resolved: map[string]string{}, Refused: map[string]AdoptionRefusal{}, Leased: map[string]struct{}{}}
	for _, id := range adopted {
		r.Adopted[id] = f.appID
	}
	for _, id := range leased {
		r.Leased[id] = struct{}{}
	}
	for id, reason := range refused {
		r.Refused[id] = AdoptionRefusal{AppID: f.appID, Reason: reason}
	}
	return r
}

func (f *adoptionFixture) state(t *testing.T, id string) state.State {
	t.Helper()
	row, err := f.store.InstanceByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return state.State(row.State)
}

func TestAdoptionReportFailsGuestsTheRestartedVMMDDoesNotHold(t *testing.T) {
	f := newAdoptionFixture(t)
	adopted := f.instance(t, state.StateRunning)
	refused := f.instance(t, state.StateRunning)
	unrecorded := f.instance(t, state.StateRunning)
	created := f.instance(t, state.StateRunning) // woken by the successor
	frozen := f.instance(t, state.StateFrozen)
	waking := f.instance(t, state.StateWaking)
	f.restart(t)
	rep := f.report([]string{adopted.ID}, []string{created.ID}, map[string]string{refused.ID: "process_gone"})
	f.vmm.report = func() (AdoptionReport, error) { return rep, nil }

	moved, err := f.rec.Reconcile(context.Background())
	if err != nil || moved != 3 {
		t.Fatalf("moved %d, %v; want 3", moved, err)
	}
	for _, c := range []struct {
		name string
		id   string
		want state.State
	}{
		{"adopted", adopted.ID, state.StateRunning},
		{"refused", refused.ID, state.StateFailed},
		{"unrecorded", unrecorded.ID, state.StateFailed},
		{"created by the successor", created.ID, state.StateRunning},
		{"frozen, not adopted", frozen.ID, state.StateParked},
		{"waking: its wake owns it", waking.ID, state.StateWaking},
	} {
		if got := f.state(t, c.id); got != c.want {
			t.Errorf("%s: state %s, want %s", c.name, got, c.want)
		}
	}
	for _, id := range []string{refused.ID, unrecorded.ID} {
		if f.engine.Ledger().ResidentFor(id) {
			t.Errorf("%s still reserves capacity", id)
		}
	}
	if !f.engine.Ledger().ResidentFor(adopted.ID) {
		t.Error("adopted guest lost its reservation")
	}

	// Applied once per epoch.
	if _, err := f.rec.Reconcile(context.Background()); err != nil || f.vmm.fetches != 1 {
		t.Fatalf("second tick fetched %d reports, %v", f.vmm.fetches, err)
	}
	// The next restart is applied again.
	f.restart(t)
	rep = f.report(nil, nil, nil)
	if moved, err := f.rec.Reconcile(context.Background()); err != nil || moved != 2 || f.vmm.fetches != 2 {
		t.Fatalf("next epoch: moved %d, fetches %d, %v", moved, f.vmm.fetches, err)
	}
	for _, id := range []string{adopted.ID, created.ID} {
		if got := f.state(t, id); got != state.StateFailed {
			t.Fatalf("guest the second restart did not hold: %s", got)
		}
	}
}

// A frozen row whose freeze or thaw the restart cut short parks: its guest
// now runs. A completed freeze the vmmd adopted stays frozen.
func TestAdoptionReportParksAFrozenRowWhoseGuestWasThawed(t *testing.T) {
	f := newAdoptionFixture(t)
	thawed := f.instance(t, state.StateFrozen)
	frozen := f.instance(t, state.StateFrozen)
	running := f.instance(t, state.StateRunning)
	f.restart(t)
	rep := f.report([]string{thawed.ID, frozen.ID, running.ID}, nil, nil)
	rep.Resolved[thawed.ID] = AdoptionResolvedThawed
	rep.Resolved[running.ID] = AdoptionResolvedThawed
	f.vmm.report = func() (AdoptionReport, error) { return rep, nil }
	if moved, err := f.rec.Reconcile(context.Background()); err != nil || moved != 1 {
		t.Fatalf("moved %d, %v; want 1", moved, err)
	}
	for id, want := range map[string]state.State{thawed.ID: state.StateParked, frozen.ID: state.StateFrozen, running.ID: state.StateRunning} {
		if got := f.state(t, id); got != want {
			t.Errorf("%s: %s, want %s", id, got, want)
		}
	}
}

func TestAdoptionReportActsOnlyOnMatchingCompleteReports(t *testing.T) {
	for _, c := range []struct {
		name      string
		mutate    func(*AdoptionReport)
		err       error
		refetches bool
	}{
		{name: "incomplete", mutate: func(r *AdoptionReport) { r.Complete = false }},
		{name: "older epoch", mutate: func(r *AdoptionReport) { r.Epoch-- }, refetches: true},
		{name: "other incarnation", mutate: func(r *AdoptionReport) { r.Incarnation = uuid.NewString() }, refetches: true},
		{name: "adoption off", err: ErrAdoptionReportOff},
		{name: "vmmd unavailable", err: errors.New("unavailable"), refetches: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newAdoptionFixture(t)
			lost := f.instance(t, state.StateRunning)
			f.restart(t)
			rep := f.report(nil, nil, nil)
			if c.mutate != nil {
				c.mutate(&rep)
			}
			f.vmm.report = func() (AdoptionReport, error) { return rep, c.err }
			moved, _ := f.rec.Reconcile(context.Background())
			if moved != 0 || f.state(t, lost.ID) != state.StateRunning {
				t.Fatalf("moved %d, state %s; want nothing applied", moved, f.state(t, lost.ID))
			}
			_, _ = f.rec.Reconcile(context.Background())
			if want := map[bool]int{true: 2, false: 1}[c.refetches]; f.vmm.fetches != want {
				t.Fatalf("fetches = %d, want %d", f.vmm.fetches, want)
			}
		})
	}
}

func TestAdoptionReportLeavesARowWokenAgainAfterTheList(t *testing.T) {
	f := newAdoptionFixture(t)
	ins := f.instance(t, state.StateRunning)
	f.restart(t)
	listed := ins
	listed.WakeID = uuid.NewString() // The row's wake changed after the list.
	moved, retry := f.engine.applyAdoptionReport(context.Background(), f.report(nil, nil, nil), []state.Instance{listed})
	if moved != 0 || !retry || f.state(t, ins.ID) != state.StateRunning || f.vmm.destroys != 0 {
		t.Fatalf("moved %d retry %v state %s destroys %d; want the re-woken row left alone for a fresh pass",
			moved, retry, f.state(t, ins.ID), f.vmm.destroys)
	}
}

// rebindStore records re-binds and refuses those named in refuse.
type rebindStore struct {
	MemStore *state.MemStore
	hops     map[string][]state.ApplicationStandardRebind
	refuse   map[string]error
	recorded []state.ApplicationStandardRebind
}

func (s *rebindStore) ListApplicationStandardRebinds(_ context.Context, id string) ([]state.ApplicationStandardRebind, error) {
	return s.hops[id], nil
}

func (s *rebindStore) RecordApplicationStandardRebind(_ context.Context, h state.ApplicationStandardRebind) error {
	if err := s.refuse[h.InstanceID]; err != nil {
		return err
	}
	s.recorded = append(s.recorded, h)
	s.hops[h.InstanceID] = append(s.hops[h.InstanceID], h)
	return nil
}

// Each adopted admission is re-bound from its current holder to the report's
// vmmd; a refused hop moves the guest to refused, a transient one retries.
func TestAdoptionReportRebindsAdoptedAdmissions(t *testing.T) {
	a, x, b := uuid.NewString(), uuid.NewString(), uuid.NewString()
	fresh, chained, done, refused, flaky := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	fake := &rebindStore{MemStore: state.NewMemStore(), hops: map[string][]state.ApplicationStandardRebind{
		chained: {{FromIncarnation: a, ToIncarnation: x}},
		done:    {{FromIncarnation: a, ToIncarnation: b}},
	}, refuse: map[string]error{refused: state.ErrApplicationStandardRuntimeStale, flaky: errors.New("connection reset")}}
	e := newEngine(t, statetest.NewMem(fake), &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	adm := AdoptionAdmission{Token: uuid.NewString(), IssuingIncarnation: a, CompletedAtUnixNano: 7, Netns: "fc-1", HostIP: "10.0.0.1", LeaseUID: 20001}
	rep := AdoptionReport{NodeID: uuid.NewString(), Epoch: 4, Incarnation: b, Adopted: map[string]string{}, Refused: map[string]AdoptionRefusal{}, Admissions: map[string]AdoptionAdmission{}}
	for _, id := range []string{fresh, chained, done, refused, flaky} {
		rep.Adopted[id], rep.Admissions[id] = "app", adm
	}
	rebound, retry := e.rebindAdoptedAdmissions(t.Context(), &rep)
	if rebound != 2 || !retry {
		t.Fatalf("rebound %d retry %v; want 2 and a retry", rebound, retry)
	}
	from := map[string]string{}
	for _, h := range fake.recorded {
		if h.ToIncarnation != b || h.VMMDEpoch != 4 || h.NodeID != rep.NodeID || h.Observed.Token != adm.Token || h.Observed.LeaseUID != adm.LeaseUID {
			t.Fatalf("hop = %+v", h)
		}
		from[h.InstanceID] = h.FromIncarnation
	}
	if from[fresh] != a || from[chained] != x || len(from) != 2 {
		t.Fatalf("hops from = %v; want the issuer for a fresh admission and the chain head for a re-bound one", from)
	}
	if _, ok := rep.Adopted[refused]; ok || rep.Refused[refused].Reason != AdoptionRebindRefusedReason {
		t.Fatalf("refused re-bind left the guest adopted: %+v", rep.Refused[refused])
	}
	if _, ok := rep.Adopted[flaky]; !ok {
		t.Fatal("a transient failure dropped the guest")
	}
	// The next pass finds both re-bound and records nothing new.
	delete(fake.refuse, flaky)
	fake.recorded = nil
	if rebound, retry := e.rebindAdoptedAdmissions(t.Context(), &rep); rebound != 1 || retry || len(fake.recorded) != 1 || fake.recorded[0].InstanceID != flaky {
		t.Fatalf("second pass rebound %d retry %v recorded %+v", rebound, retry, fake.recorded)
	}
}

// A guest whose admission cannot be re-bound is repaired like a refused one.
func TestAdoptionReportRepairsAnAdmissionThatCannotBeRebound(t *testing.T) {
	f := newAdoptionFixture(t)
	f.restart(t)
	ins := f.instance(t, state.StateRunning)
	rep := f.report([]string{ins.ID}, nil, nil)
	rep.Admissions = map[string]AdoptionAdmission{ins.ID: {Token: uuid.NewString(), IssuingIncarnation: uuid.NewString(), CompletedAtUnixNano: 1, Netns: "fc-x", HostIP: "10.0.0.2", LeaseUID: 20002}}
	f.vmm.report = func() (AdoptionReport, error) { return rep, nil }
	if _, err := f.rec.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.state(t, ins.ID); got != state.StateFailed {
		t.Fatalf("row %s after a refused re-bind; want failed", got)
	}
}
