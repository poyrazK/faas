//go:build linux && metal

package fcvm

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1747 — different workload kinds must contend at the real native owner
// boundary. The loser cannot allocate a journal, slot, network or guest.
func TestMetalFinancialComputePostgresAppJobContention(t *testing.T) {
	f := newFinancialNativeFixture(t)
	f.configureJob(0)
	owners := []*financialNativeOwner{f.start(0, ""), f.start(1, "")}
	f.activateJobAccount(20400, 1)
	f.claimJob(0)
	var results [2]financialNativeEvent
	var wg sync.WaitGroup
	for i, o := range owners {
		wg.Go(func() { results[i] = o.call(t, financialNativeCommand{Operation: "wake"}) })
	}
	wg.Wait()
	winner := -1
	for i, result := range results {
		if result.Error == "" {
			if winner != -1 || result.Live != 1 || result.Leased != 1 {
				t.Fatalf("mixed account overspend: %+v", results)
			}
			winner = i
		} else if result.Error != "budget" || result.Live != 0 || result.Leased != 0 || result.Journal {
			t.Fatalf("unfunded native effects: %+v", results)
		}
	}
	if winner < 0 {
		t.Fatalf("no funded native start: %+v", results)
	}
	l := f.lease(owners[winner])
	f.assertJobAccountFunding(l)
	owners[winner].must(t, "destroy")
	f.assertSettled(owners[winner], l)
	b, err := f.store.FinancialComputeBalance(f.ctx, f.policy, time.Now())
	if err != nil || b.AllowanceReserved != 0 || b.AllowanceCommitted == 0 || b.UnconfirmedLeases != 0 {
		t.Fatalf("mixed native settlement: %+v %v", b, err)
	}
}

func TestMetalFinancialComputePostgresJobDeadline(t *testing.T) {
	f := newFinancialNativeFixture(t)
	f.configureJob(0)
	owner := f.start(0, "")
	f.start(1, "")
	f.activateJobAccount(1000000, 1000)
	f.claimJob(0)
	owner.must(t, "wake")
	owner.must(t, "offline")
	l := f.lease(owner)
	f.assertJobAccountFunding(l)
	owner.wait(t, l.FundedUntil, func(e financialNativeEvent) bool { return e.Physical == 0 && e.ResourcesRemoved && e.Leased > 0 })
	f.assertJobAccountFunding(l)
	if current := f.lease(owner); current.SettledAt != nil {
		t.Fatal("job DB outage freed physical funding")
	}
	owner.kill(t)
	replacement := f.start(0, "")
	replacement.must(t, "recover")
	f.assertSettled(replacement, l)
	if replay := replacement.call(t, financialNativeCommand{Operation: "wake"}); replay.Error != "fence" || replay.Leased != 0 {
		t.Fatalf("settled job revived after restart: %+v", replay)
	}
}

func TestMetalFinancialComputePostgresJobRecovery(t *testing.T) {
	for _, phase := range []string{"boot", "live", "teardown", "failed_kill"} {
		t.Run(phase, func(t *testing.T) {
			f := newFinancialNativeFixture(t)
			f.configureJob(0)
			checkpoint := phase
			if phase == "failed_kill" {
				checkpoint = ""
			}
			owner := f.start(0, checkpoint)
			f.start(1, "")
			f.activateJobAccount(1000000, 1000)
			f.claimJob(0)
			owner.send(t, financialNativeCommand{Operation: "wake"})
			if phase == "boot" {
				if e := owner.receive(t); e.Operation != "checkpoint" {
					t.Fatalf("job boot checkpoint missing: %+v", e)
				}
			} else {
				if e := owner.receive(t); e.Error != "" || e.Live != 1 {
					t.Fatalf("real job boot: %+v", e)
				}
				if phase == "teardown" {
					owner.send(t, financialNativeCommand{Operation: "destroy"})
					if e := owner.receive(t); e.Operation != "checkpoint" || !e.ResourcesRemoved {
						t.Fatalf("job retirement checkpoint missing: %+v", e)
					}
				}
				if phase == "failed_kill" {
					owner.must(t, "kill-off")
					e := owner.call(t, financialNativeCommand{Operation: "destroy"})
					if e.Error == "" || e.Leased == 0 || e.ResourcesRemoved {
						t.Fatalf("failed job kill freed ownership: %+v", e)
					}
				}
			}
			l := f.lease(owner)
			f.assertJobAccountFunding(l)
			owner.kill(t)
			replacement := f.start(0, "")
			if replacement.identity.Incarnation == owner.identity.Incarnation {
				t.Fatal("job owner reused native incarnation")
			}
			replacement.must(t, "settlement-off")
			e := replacement.call(t, financialNativeCommand{Operation: "recover"})
			if e.Error == "" || !e.ResourcesRemoved || e.Leased == 0 {
				t.Fatalf("failed job settlement released native ownership: %+v", e)
			}
			f.assertJobAccountFunding(l)
			replacement.must(t, "settlement-on")
			replacement.must(t, "recover")
			f.assertSettled(replacement, l)
		})
	}
}

func (f *financialNativeFixture) configureJob(index int) {
	f.t.Helper()
	j, err := f.store.JobCreate(f.ctx, f.app.AccountID, "native-funded-job", "batch", "acceptance/job", []string{"/bin/sh", "-c", "sleep 3600"}, 128, 300, 1, 0, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	r, _, err := f.store.JobRunCreate(f.ctx, j.ID, f.app.AccountID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		f.t.Fatal(err)
	}
	f.configs[index].AppID = ""
	f.configs[index].JobID = j.ID
	f.configs[index].JobRunID = r.ID
	f.configs[index].JobLeaseToken = uuid.NewString()
}

func (f *financialNativeFixture) activateJobAccount(grant, limit int64) {
	f.t.Helper()
	a, err := f.store.AccountByID(f.ctx, f.app.AccountID)
	if err != nil {
		f.t.Fatal(err)
	}
	for at := a.CreatedAt.Truncate(time.Minute); at.Before(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
		if err := f.store.RecordFinancialSamplingWindow(f.ctx, at, true, false); err != nil {
			f.t.Fatal(err)
		}
	}
	month := financial.ComputeMonth(time.Now())
	_, err = f.store.PutFinancialPriceSnapshot(f.ctx, state.FinancialPriceSnapshot{AccountID: a.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), EffectiveFrom: month, Plan: api.PlanHobby, DeliveryMode: "live", Price: financial.Price{Version: "native-job-account", Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: grant}})
	if err != nil {
		f.t.Fatal(err)
	}
	f.policy, err = f.store.CreateFinancialBudget(f.ctx, a.ID, uuid.NewString(), "native-acceptance", financial.BudgetSpec{Name: "native account job compute", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"}, Basis: "net_usage", LimitMillicents: limit, NotifyMillicents: []int64{}, Mode: "strict", Action: "suspend_workloads", ResumeRule: "manual", Enabled: true})
	if err != nil {
		f.t.Fatal(err)
	}
	for _, cfg := range f.configs {
		if cfg.JobID == "" {
			continue
		}
		_, err = f.store.CreateFinancialBudget(f.ctx, a.ID, uuid.NewString(), "native-acceptance", financial.BudgetSpec{Name: "native job gross compute", Scope: financial.BudgetScope{Kind: "job", ID: cfg.JobID}, Currency: "USD", Meters: []string{"compute"}, Basis: "gross_usage", LimitMillicents: 1000, NotifyMillicents: []int64{}, Mode: "strict", Action: "suspend_workloads", ResumeRule: "manual", Enabled: true})
		if err != nil {
			f.t.Fatal(err)
		}
	}

}

func (f *financialNativeFixture) claimJob(index int) {
	f.t.Helper()
	cfg := f.configs[index]
	if err := f.store.PrepareFinancialJobCompute(f.ctx, cfg.AccountID, cfg.JobID); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.store.CreateAndClaimJobInstance(f.ctx, cfg.Instance, cfg.JobID, cfg.JobRunID, 0, string(state.StateColdBooting), 128, cfg.NodeID, cfg.Instance, cfg.JobLeaseToken, time.Now().Add(10*time.Minute), cfg.NodeID); err != nil {
		f.t.Fatal(err)
	}
}

func (f *financialNativeFixture) assertJobAccountFunding(l financial.ComputeLease) {
	f.t.Helper()
	b, err := f.store.FinancialComputeBalance(f.ctx, f.policy, time.Now())
	if err != nil || !l.AccountFunded || l.QuantityReserved <= 0 || b.AllowanceReserved != min(b.AllowanceGranted, l.QuantityReserved) || b.UnconfirmedLeases != 1 {
		f.t.Fatalf("native shared job funding: lease=%+v balance=%+v %v", l, b, err)
	}
}
