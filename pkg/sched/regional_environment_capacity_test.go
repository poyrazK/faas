package sched

// adr: 791
import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestEnvironmentAdmissionRecoveryFlagCannotCreateOverCapRuntime(t *testing.T) {
	l := NewNodeLedger()
	r := Request{Instance: "original", AppID: "app", EnvironmentKey: "environment:stage", Plan: api.PlanPro, RAMMB: 128, MaxConcurrency: 1}
	if err := l.Admit(r); err != nil {
		t.Fatal(err)
	}
	r.Instance, r.AllowConcurrencyRecovery = "new", true
	if err := l.Admit(r); err == nil {
		t.Fatal("public recovery flag created over-cap resident")
	}
	if l.ResidentFor(r.Instance) || l.ConcurrencyForEnvironment("app", r.EnvironmentKey) != 1 {
		t.Fatal("refused public request mutated environment accounting")
	}
	if err := l.recoverResident(r); err != nil {
		t.Fatal("private observed recovery dropped known resident", err)
	}
	if l.ConcurrencyForEnvironment("app", r.EnvironmentKey) != 2 {
		t.Fatal("private recovery hid over-cap debt")
	}
}

func TestEnvironmentSharedCeilingCountsOriginalRegionalHoldAcrossIDSpelling(t *testing.T) {
	f := newRegionalWarmFixture(t)
	f.prepare(t)
	l := f.engine.ledger
	if l.servingEnvironmentConcurrency(f.app.ID, "", true) != 1 {
		t.Fatal("regional resume did not charge its original production environment")
	}
	compact := strings.ReplaceAll(f.app.ID, "-", "")
	for i := 0; i < api.MustLimitsFor(f.acct.Plan).MaxConcurrency-1; i++ {
		if err := l.Admit(Request{Instance: uuid.NewString(), AppID: compact, EnvironmentKey: "environment:" + uuid.NewString(), Plan: f.acct.Plan, RAMMB: 128, VCPU: 1, MaxConcurrency: 1}); err != nil {
			t.Fatal("valid shared capacity refused", err)
		}
	}
	r := Request{Instance: uuid.NewString(), AppID: compact, EnvironmentKey: "environment:production", ProductionEnvironment: true, Plan: f.acct.Plan, RAMMB: 128, VCPU: 1, MaxConcurrency: 1, AllowConcurrencyRecovery: true}
	if err := l.Admit(r); err == nil {
		t.Fatal("fresh environment or compact app spelling hid retained regional hold")
	}
	if l.ResidentFor(r.Instance) || l.Concurrency(f.app.ID) != api.MustLimitsFor(f.acct.Plan).MaxConcurrency {
		t.Fatal("refusal changed shared application accounting")
	}
	if f.vmm.destroys != 0 {
		t.Fatal("shared capacity refusal retired original regional resident")
	}
}
