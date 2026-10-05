package sched

// adr: 627
import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

type regionalWarmFixture struct {
	store    *state.MemStore
	acct     state.Account
	app      state.App
	dep      state.Deployment
	warm     state.Instance
	grant    runtimequota.Grant
	epoch    *runtimequota.HostEpoch
	vmm      *regionalWarmFakeVMM
	engine   *Engine
	physical runtimequota.CapacityPermit
}

type regionalWarmFakeVMM struct {
	*warmResumeFakeVMM
	placements []runtimequota.Placement
	resumeHook func(runtimequota.Placement) error
}

func (f *regionalWarmFakeVMM) ResumeRegionalWarmInstance(_ context.Context, node, instance string, p runtimequota.Placement) error {
	if !runtimequota.MatchesID(p.Permit.NodeID, node) || !runtimequota.MatchesID(p.Permit.InstanceID, instance) {
		return runtimequota.ErrBinding
	}
	f.placements = append(f.placements, p.Clone())
	if f.resumeHook != nil {
		return f.resumeHook(p)
	}
	return nil
}

func newRegionalWarmFixture(t *testing.T) regionalWarmFixture {
	t.Helper()
	s, acct, app, dep := regionalQuotaSchedulerFixture(t)
	app.WarmPoolSize = 1
	region := "eu-fsn"
	node, err := s.CreateComputeNode(t.Context(), state.ComputeNode{ID: uuid.NewString(), Name: "regional-warm", TargetURL: "unix:///run/faas/warm-test.sock", Active: true, Region: &region})
	if err != nil {
		t.Fatal(err)
	}
	warm, err := s.CreateInstanceWithMode(t.Context(), app.ID, dep.ID, string(state.StateWarm), app.RAMMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetInstanceRuntime(t.Context(), warm.ID, "fc-"+warm.ID, "10.100.0.2", 20001); err != nil {
		t.Fatal(err)
	}
	warm, err = s.InstanceByID(t.Context(), warm.ID)
	if err != nil {
		t.Fatal(err)
	}
	epoch := &runtimequota.HostEpoch{NodeID: node.ID, HostEpochID: uuid.NewString()}
	vmm := &regionalWarmFakeVMM{warmResumeFakeVMM: &warmResumeFakeVMM{fakeVMM: &fakeVMM{statsHook: func(context.Context, string) (*StatsSnapshot, error) {
		return &StatsSnapshot{RegionalRuntimeHostEpoch: epoch}, nil
	}}}}
	e := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
	freezeRegionalSchedulerApp(t, s, acct, app)
	head, err := s.RegionalRuntimeQuotaHead(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := e.issueRegionalRuntimeLaunch(t.Context(), node.ID, warm.ID, warm.WakeID, runtimequota.CapacityWarm, AppSpec{AppID: app.ID, AccountID: acct.ID, DeploymentID: dep.ID, Plan: acct.Plan}, head.Grant)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SeedLedger(t.Context()); err != nil || e.ledger.Concurrency(app.ID) != 0 {
		t.Fatal("paused resident counted as serving", err)
	}
	return regionalWarmFixture{store: s, acct: acct, app: app, dep: dep, warm: warm, grant: head.Grant, epoch: epoch, vmm: vmm, engine: e, physical: issued.RegionalRuntime.Permit}
}

func (f regionalWarmFixture) prepare(t *testing.T) runtimequota.Placement {
	t.Helper()
	p, err := f.engine.prepareRegionalWarmResume(t.Context(), f.app, f.acct, f.dep, f.warm, f.grant)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRegionalWarmResumeRetainsAttemptAcrossSchedulers(t *testing.T) {
	f := newRegionalWarmFixture(t)
	before, err := f.store.RegionalRuntimeCapacity(t.Context(), f.app.ID, f.physical.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := f.prepare(t)
	if f.engine.ledger.Concurrency(f.app.ID) != 1 || !f.engine.ledger.regionalWarmResumePending(f.warm.ID) {
		t.Fatal("resume was issued before local serving reservation")
	}
	// A local retry is permitted at the cap, on the same physical identity.
	f.app.MaxConcurrency = 1
	again := f.prepare(t)
	if *again.Seat != *p.Seat || again.Permit != p.Permit {
		t.Fatal("retry minted a new authorization")
	}
	fresh := newEngine(t, f.store, f.vmm, &fakeNotifier{}, "1.10.0")
	if err := fresh.SeedLedger(t.Context()); err != nil || fresh.ledger.Concurrency(f.app.ID) != 1 || !fresh.ledger.regionalWarmResumePending(f.warm.ID) {
		t.Fatal("restart forgot escaped warm resume", err)
	}
	f.engine = fresh
	again = f.prepare(t)
	if *again.Seat != *p.Seat {
		t.Fatal("peer minted another seat")
	}
	after, err := f.store.RegionalRuntimeCapacity(t.Context(), f.app.ID, f.physical.ID)
	if err != nil || before.Permit != after.Permit || !before.CreatedAt.Equal(after.CreatedAt) || !before.IssuedAt.Equal(*after.IssuedAt) || after.ReleasedAt != nil {
		t.Fatal("resume changed original physical receipt", err)
	}
	if usage, err := f.store.RegionalRuntimeServingSeatUsage(t.Context(), f.app.ID, f.grant.AllocationID); err != nil || usage.TotalHeld != 1 {
		t.Fatal("retry duplicated durable serving hold", usage, err)
	}
}

type uncertainRegionalWarmStore struct {
	state.Store
	afterCommit bool
	err         error
}

func (s uncertainRegionalWarmStore) IssueRegionalRuntimeServingResume(ctx context.Context, i runtimequota.ServingResumeIntent) (state.RegionalRuntimeServingResumeReceipt, error) {
	if s.afterCommit {
		if _, err := s.Store.IssueRegionalRuntimeServingResume(ctx, i); err != nil {
			return state.RegionalRuntimeServingResumeReceipt{}, err
		}
	}
	return state.RegionalRuntimeServingResumeReceipt{}, s.err
}

func TestRegionalWarmResumeUncertainIssuanceAndDispatchRetainHolds(t *testing.T) {
	for _, afterCommit := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-commit", true: "lost-commit-reply"}[afterCommit], func(t *testing.T) {
			f := newRegionalWarmFixture(t)
			lost := errors.New("response lost")
			f.engine.store = uncertainRegionalWarmStore{Store: f.store, afterCommit: afterCommit, err: lost}
			if _, err := f.engine.prepareRegionalWarmResume(t.Context(), f.app, f.acct, f.dep, f.warm, f.grant); !errors.Is(err, lost) {
				t.Fatal(err)
			}
			if f.engine.ledger.Concurrency(f.app.ID) != 1 || f.vmm.destroys != 0 || len(f.vmm.placements) != 0 {
				t.Fatal("uncertainty freed concurrency or dispatched work")
			}
			f.engine.store = struct{ state.Store }{f.store}
			f.app.MaxConcurrency = 1
			p := f.prepare(t)
			retained, err := f.store.HeldRegionalRuntimeServingResumeForInstance(t.Context(), f.app.ID, p.Permit.InstanceID, p.Permit.NodeID, p.Permit.RunID)
			if err != nil || retained.Intent.Seat != *p.Seat {
				t.Fatal("caller did not use retained intent", err)
			}
			f.vmm.resumeHook = func(sent runtimequota.Placement) error {
				if f.engine.ledger.Concurrency(f.app.ID) != 1 || *sent.Seat != retained.Intent.Seat {
					t.Fatal("dispatch lacked retained reservation")
				}
				return lost
			}
			if err := f.engine.dispatchRegionalWarmResume(t.Context(), f.warm, p); !errors.Is(err, lost) {
				t.Fatal(err)
			}
			if f.vmm.resumeCalls != 0 || f.vmm.destroys != 0 || f.engine.ledger.Concurrency(f.app.ID) != 1 {
				t.Fatal("uncertain resume fell back or released")
			}
			f.vmm.resumeHook = nil
			if err := f.engine.dispatchRegionalWarmResume(t.Context(), f.warm, f.prepare(t)); err != nil || len(f.vmm.placements) != 2 || *f.vmm.placements[0].Seat != *f.vmm.placements[1].Seat {
				t.Fatal("transport retry changed intent", err)
			}
			warm, err := f.store.InstanceByID(t.Context(), f.warm.ID)
			if err != nil || warm.State != string(state.StateWarm) || f.engine.ledger.Concurrency(f.app.ID) != 1 {
				t.Fatal("transport acknowledgement published or released hold", err)
			}
		})
	}
}

func TestRegionalWarmResumeRefusesWrongPhysicalIdentity(t *testing.T) {
	for _, fail := range []string{"account", "app", "deployment", "plan", "node", "epoch", "run", "netns", "node-region", "receipt-read", "local-cap"} {
		t.Run(fail, func(t *testing.T) {
			f := newRegionalWarmFixture(t)
			switch fail {
			case "account":
				f.acct.ID = uuid.NewString()
			case "app":
				f.warm.AppID = uuid.NewString()
			case "deployment":
				f.dep.ID = uuid.NewString()
			case "plan":
				f.acct.Plan = api.PlanScale
			case "node":
				f.warm.NodeID = uuid.NewString()
			case "epoch":
				f.epoch.HostEpochID = uuid.NewString()
			case "run":
				f.warm.WakeID = uuid.NewString()
			case "netns":
				f.warm.Netns = ""
			case "node-region":
				f.grant.Binding.Region = "us-iad"
			case "receipt-read":
				f.engine.store = unavailableRegionalWarmReader{Store: f.store, err: errors.New("storage unavailable")}
			case "local-cap":
				f.app.MaxConcurrency = 1
				if err := f.engine.ledger.Admit(Request{Instance: uuid.NewString(), AppID: f.app.ID, DeploymentID: f.dep.ID, Plan: f.acct.Plan, RAMMB: f.app.RAMMB, NodeID: f.warm.NodeID, MaxConcurrency: 1}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.engine.prepareRegionalWarmResume(t.Context(), f.app, f.acct, f.dep, f.warm, f.grant); err == nil {
				t.Fatal("unbound resume was issued")
			}
			usage, err := f.store.RegionalRuntimeServingSeatUsage(t.Context(), f.app.ID, f.grant.AllocationID)
			if err != nil || usage.TotalHeld != 0 || f.engine.ledger.regionalWarmResumePending(f.warm.ID) || f.vmm.destroys != 0 || len(f.vmm.placements) != 0 {
				t.Fatal("refusal issued, dispatched or removed a hold", usage, err)
			}
		})
	}
}

type unavailableRegionalWarmReader struct {
	state.Store
	err error
}

func (s unavailableRegionalWarmReader) RegionalRuntimeCapacity(context.Context, string, string) (state.RegionalRuntimeCapacityReceipt, error) {
	return state.RegionalRuntimeCapacityReceipt{}, s.err
}

func (s unavailableRegionalWarmReader) HeldRegionalRuntimeServingResumeForInstance(context.Context, string, string, string, string) (state.RegionalRuntimeServingResumeReceipt, error) {
	return state.RegionalRuntimeServingResumeReceipt{}, s.err
}

func TestRegionalWarmResumeRecoveryRetainsFencesAndStopsOnAmbiguity(t *testing.T) {
	for _, variant := range []string{"issued", "seat-fenced", "physical-fenced", "released", "unknown", "ambiguous"} {
		t.Run(variant, func(t *testing.T) {
			f := newRegionalWarmFixture(t)
			p := f.prepare(t)
			fence := uuid.NewString()
			switch variant {
			case "seat-fenced", "released":
				if _, err := f.store.FenceRegionalRuntimeServingSeat(t.Context(), f.app.ID, p.Seat.ID, fence); err != nil {
					t.Fatal(err)
				}
				if variant == "released" {
					releaseRegionalWarmTestSeat(t, f, *p.Seat, fence)
				}
			case "physical-fenced":
				if _, err := f.store.FenceRegionalRuntimeCapacity(t.Context(), f.app.ID, p.Permit.ID, fence); err != nil {
					t.Fatal(err)
				}
			case "ambiguous":
				other := p.Permit
				other.ID, other.HostEpochID = uuid.NewString(), uuid.NewString()
				if err := f.store.IssueRegionalRuntimePlacement(t.Context(), runtimequota.Placement{Permit: other}); err != nil {
					t.Fatal(err)
				}
				i, err := runtimequota.NewServingResumeIntent(other, uuid.NewString(), false)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.IssueRegionalRuntimeServingResume(t.Context(), i); err != nil {
					t.Fatal(err)
				}
			}
			var store state.Store = f.store
			if variant == "unknown" {
				store = unavailableRegionalWarmReader{Store: store, err: errors.New("unknown inventory")}
			}
			restarted := newEngine(t, store, f.vmm, &fakeNotifier{}, "1.10.0")
			err := restarted.SeedLedger(t.Context())
			blocked := variant == "unknown" || variant == "ambiguous"
			if (err != nil) != blocked {
				t.Fatal("unexpected recovery status", err)
			}
			if blocked && !errors.Is(err, ErrRegionalRuntimeRecoveryUnresolved) {
				t.Fatal("incomplete accounting did not block production startup", err)
			}
			want := 1
			if variant == "released" {
				want = 0
			}
			if restarted.ledger.Concurrency(f.app.ID) != want || restarted.ledger.ResidentRAM() != f.warm.RAMMB+api.PerVMOverheadMB || f.vmm.destroys != 0 {
				t.Fatal("restart undercounted resident or escaped serving hold")
			}
		})
	}
}

// Signed assertions exercise bookkeeping only; they are not native pause proof.
func releaseRegionalWarmTestSeat(t *testing.T, f regionalWarmFixture, seat runtimequota.ServingSeat, fence string) {
	t.Helper()
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := runtimequota.NewSigner(f.grant.Binding.CellID, private)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Cell, IssuerID: f.grant.Binding.CellID, Public: pub}})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := signer.SignServingSeatStop(runtimequota.ServingSeatStop{Seat: seat, FenceID: fence, AdmissionFrozen: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (state.RegionalRuntimeCapacityAuthority{Store: f.store, Cells: trust}).AcceptServingSeatStop(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
}

func TestRegionalWarmResumeProductionGateAndLegacyCleanupRemainFrozen(t *testing.T) {
	f := newRegionalWarmFixture(t)
	f.prepare(t)
	if _, _, err := f.engine.promoteWarmInstanceLocked(t.Context(), f.app, f.acct, api.MustLimitsFor(f.acct.Plan), f.dep, string(state.InstanceModeNormal)); !errors.Is(err, state.ErrRegionalRuntimeQuotaFrozen) {
		t.Fatal("bookkeeping activated promotion", err)
	}
	if f.engine.discardWarmPromotion(t.Context(), f.warm, "test-uncertainty") || f.vmm.destroys != 0 || len(f.vmm.placements) != 0 || f.engine.ledger.Concurrency(f.app.ID) != 1 {
		t.Fatal("frozen gate cleaned up an uncertain resident")
	}
	// A legacy-only transport never gets a regional resume fallback.
	f.engine.vmm = f.vmm.warmResumeFakeVMM
	p := f.prepare(t)
	if err := f.engine.dispatchRegionalWarmResume(t.Context(), f.warm, p); err == nil || f.vmm.resumeCalls != 0 {
		t.Fatal("missing regional capability fell back", err)
	}
}
