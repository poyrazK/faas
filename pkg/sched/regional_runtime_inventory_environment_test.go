package sched

// adr: 792
import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRegionalRuntimeInventoryEnvironmentCountsEachPhysicalHoldAndRow(t *testing.T) {
	s, app, _, source, resources, _ := regionalAccountingResidentFixture(t, true)
	issueAccountingFixture(t, s, source, resources)
	other := source
	other.Permit.ID, other.Permit.HostEpochID = uuid.NewString(), uuid.NewString()
	issueAccountingFixture(t, s, other, resources)
	e := newEngine(t, s, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if err := e.SeedLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	l := e.ledger
	// Rebuilding omitted literal-ledger indexes includes original holds as well
	// as advisory rows; host epochs sharing a lookup remain independent.
	l.perAppEnvironment, l.perAppProduction = nil, nil
	if l.servingEnvironmentConcurrency(app.ID, "", true) != 3 || l.ConcurrencyForEnvironment(app.ID, "") != 3 {
		t.Fatal("startup hid a physical serving environment charge")
	}
	request := Request{Instance: uuid.NewString(), AppID: strings.ReplaceAll(app.ID, "-", ""), EnvironmentKey: "environment:" + uuid.NewString(), ProductionEnvironment: true, Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 1}
	if err := l.Admit(request); err == nil {
		t.Fatal("another production alias bypassed the configured ceiling")
	}
	wrong := source
	wrong.Permit.HostEpochID = uuid.NewString()
	if l.releaseOriginalRegionalRuntime(wrong) || l.servingEnvironmentConcurrency(app.ID, "", true) != 3 {
		t.Fatal("another host epoch released original environment seats")
	}
	if !l.releaseOriginalRegionalRuntime(source) || l.servingEnvironmentConcurrency(app.ID, "", true) != 2 {
		t.Fatal("exact stop changed a replacement or advisory row charge")
	}
	if !l.releaseOriginalRegionalRuntime(other) || l.servingEnvironmentConcurrency(app.ID, "", true) != 1 {
		t.Fatal("second exact stop lost the remaining row charge")
	}
}

func TestRegionalRuntimeInventoryOriginalStageCannotAdoptRecreatedEnvironment(t *testing.T) {
	f := seedStageSnapshotPolicy(t, 0, false)
	_, _, _, source, resources := regionalAccountingFixture(t)
	p := &source.Permit
	p.Grant.Binding.RegionalAccountID = uuid.MustParse(f.account.ID).String()
	p.Grant.Binding.RegionalAppID = uuid.MustParse(f.app.ID).String()
	p.Grant.Binding.RegionalAppSlug = f.app.Slug
	p.DeploymentID = uuid.MustParse(f.stage.ID).String()
	now := time.Now()
	entry := state.RegionalRuntimeInventoryEntry{Receipt: state.RegionalRuntimeCapacityReceipt{Permit: *p, IssuedAt: &now}, Source: &source, Resources: resources, ServingSeatsHeld: 1}
	e := newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	owner, err := e.regionalInventoryEnvironment(t.Context(), entry)
	if err != nil || owner == nil || owner.production || !strings.HasPrefix(owner.key, "environment:") {
		t.Fatalf("original stage ownership: %+v %v", owner, err)
	}
	if err := e.ledger.retainRegionalInventory(entry, api.RAMAdmissionCeilingMB, owner); err != nil {
		t.Fatal(err)
	}
	r := Request{Instance: uuid.NewString(), AppID: f.app.ID, EnvironmentKey: owner.key, Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 1}
	if err := e.ledger.Admit(r); err == nil || e.ledger.Concurrency(f.app.ID) != 1 {
		t.Fatal("retained stage serving hold bypassed its configured ceiling")
	}
	f.recreateStage(t.Context(), t)
	if replacement, err := e.regionalInventoryEnvironment(t.Context(), entry); !errors.Is(err, ErrRegionalRuntimeRecoveryUnresolved) || replacement != nil {
		t.Fatal("recreated slug adopted original physical ownership", replacement, err)
	}
	if e.ledger.ConcurrencyForEnvironment(f.app.ID, owner.key) != 1 {
		t.Fatal("catalog replacement erased retained original attribution")
	}
	if !e.ledger.releaseOriginalRegionalRuntime(source) || e.ledger.ConcurrencyForEnvironment(f.app.ID, owner.key) != 0 {
		t.Fatal("exact original stop did not release retained stage attribution")
	}
}

type conflictingRegionalEnvironmentStore struct {
	state.Store
	mutate func(*state.RuntimeAppEnvSnapshot) error
}

func (s conflictingRegionalEnvironmentStore) RuntimeScalingStateForDeployment(ctx context.Context, account, app, deployment string) (state.RuntimeScalingState, error) {
	owner, err := s.Store.RuntimeScalingStateForDeployment(ctx, account, app, deployment)
	if err != nil {
		return owner, err
	}
	err = s.mutate(&owner.RuntimeAppEnvSnapshot)
	return owner, err
}

func TestRegionalRuntimeInventoryUnknownEnvironmentKeepsChargesAndClosesAdmission(t *testing.T) {
	for name, mutate := range map[string]func(*state.RuntimeAppEnvSnapshot) error{
		"account":      func(o *state.RuntimeAppEnvSnapshot) error { o.AccountID = uuid.NewString(); return nil },
		"app":          func(o *state.RuntimeAppEnvSnapshot) error { o.AppID = uuid.NewString(); return nil },
		"deployment":   func(o *state.RuntimeAppEnvSnapshot) error { o.DeploymentID = uuid.NewString(); return nil },
		"scope":        func(o *state.RuntimeAppEnvSnapshot) error { o.Scope = "stage"; return nil },
		"environment":  func(o *state.RuntimeAppEnvSnapshot) error { o.EnvironmentID = "invalid"; return nil },
		"missing pin":  func(*state.RuntimeAppEnvSnapshot) error { return state.ErrNotFound },
		"read failure": func(*state.RuntimeAppEnvSnapshot) error { return errors.New("ownership read unavailable") },
	} {
		t.Run(name, func(t *testing.T) {
			s, app, _, source, resources := regionalAccountingFixture(t)
			issueAccountingFixture(t, s, source, resources)
			reads := 0
			e := newEngine(t, conflictingRegionalEnvironmentStore{Store: s, mutate: func(owner *state.RuntimeAppEnvSnapshot) error {
				reads++
				return mutate(owner)
			}}, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			if err := e.SeedLedger(t.Context()); !errors.Is(err, ErrRegionalRuntimeRecoveryUnresolved) {
				t.Fatal("unresolved serving ownership admitted recovery", err)
			}
			if reads != 1 {
				t.Fatal("refusal did not inspect the original environment owner", reads)
			}
			if e.ledger.Concurrency(app.ID) != 1 || e.ledger.ResidentRAM() != resources.RAMMB+api.PerVMOverheadMB {
				t.Fatal("unresolved environment erased known original physical charges")
			}
			request := Request{Instance: uuid.NewString(), AppID: app.ID, Plan: api.PlanPro, RAMMB: 1, MaxConcurrency: 5, NodeID: uuid.NewString()}
			if err := e.ledger.Admit(request); !errors.Is(err, ErrRegionalRuntimeRecoveryUnresolved) {
				t.Fatal("missing attribution opened another node", err)
			}
		})
	}
}
