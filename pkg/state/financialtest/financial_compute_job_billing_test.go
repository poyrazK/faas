package financialtest

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1747 — stale residency and a deleted definition cannot bill past a
// strict job's funded envelope, or erase the job's retained compute history.
func TestFinancialComputeJobFundedBilling(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store computeTestStore = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			f := newAccountComputeFixture(t, store, 0, 1000)
			job, run := computeJob(t, f, "batch")
			r := claimComputeJob(t, f, job, run, 0)
			l, err := store.AcquireFinancialCompute(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.JobSoftDelete(t.Context(), job.ID); err != nil {
				t.Fatal(err)
			}
			var funded int64
			for at := l.StartedAt.Truncate(time.Minute); at.Before(l.FundedUntil.Add(2 * time.Minute)); at = at.Add(time.Minute) {
				seconds, err := store.FinancialComputeBillingSeconds(t.Context(), f.account.ID, "job:"+job.ID, l.InstanceID, at, 60)
				if err != nil {
					t.Fatal(err)
				}
				funded += seconds * int64(l.BillableMB)
			}
			if funded <= 0 || funded > l.QuantityReserved {
				t.Fatalf("funded job billing bound: %d > %d", funded, l.QuantityReserved)
			}
			if backend == "memory" {
				return
			} // only PostgreSQL owns the residency ledger
			observed := l.StartedAt.Add(2 * time.Minute)
			sampler := meter.NewSampler(store, nil, func() time.Time { return observed })
			for range 2 {
				if _, err := sampler.SampleJobsAndRoll(t.Context()); err != nil {
					t.Fatal(err)
				}
				observed = l.FundedUntil.Add(2 * time.Minute)
			}
			start, end := l.PeriodStart, l.PeriodStart.AddDate(0, 1, 0)
			head, err := store.FinancialEvidenceHead(t.Context(), f.account.ID, start, end)
			if err != nil {
				t.Fatal(err)
			}
			var quantity int64
			for _, row := range financialRowsWithContext(t, t.Context(), store, f.account.ID, head) {
				if row.InstanceID != l.InstanceID {
					continue
				}
				if row.Evidence.Attribution.JobID != job.ID || row.Evidence.Attribution.AppID != "" || !row.Evidence.Start.Before(l.FundedUntil) {
					t.Fatalf("billing lost funded job identity/window: %+v", row)
				}
				quantity += row.Evidence.Quantity
			}
			if quantity <= 0 || quantity > l.QuantityReserved {
				t.Fatalf("job billed %d; funded quantity %d", quantity, l.QuantityReserved)
			}
			late, err := store.FinancialComputeBillingSeconds(t.Context(), f.account.ID, "job:"+job.ID, l.InstanceID, l.FundedUntil.Add(time.Minute).Truncate(time.Minute), 60)
			if err != nil || late != 0 {
				t.Fatalf("unfunded tail billed: %d %v", late, err)
			}
		})
	}
}

// A job gross cap operates without an account cap. Deleting the policy or job
// must not make native settlement depend on customer-visible resource lookup.
func TestFinancialComputeJobStandaloneAndDeletedSettlement(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store computeTestStore = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			f := newAccountComputeFixture(t, store, 0, 1000, false)
			job, run := computeJob(t, f, "batch")
			spec := f.policy.Spec
			spec.Scope = financial.BudgetScope{Kind: "job", ID: job.ID}
			spec.Basis = "gross_usage"
			p, err := store.CreateFinancialBudget(t.Context(), f.account.ID, uuid.NewString(), "test", spec)
			if err != nil {
				t.Fatal(err)
			}
			r := claimComputeJob(t, f, job, run, 0)
			l, err := store.AcquireFinancialCompute(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}
			if !l.Strict || l.AccountFunded || l.QuantityReserved != 0 || l.Reserved <= 0 {
				t.Fatalf("standalone job cap: %+v", l)
			}
			if _, err := store.DeleteFinancialBudget(t.Context(), f.account.ID, p.ID, p.Revision, "test"); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.JobSoftDelete(t.Context(), job.ID); err != nil {
				t.Fatal(err)
			}
			cancel, err := store.CancelFinancialCompute(t.Context(), financial.ComputeRequest{NodeID: l.NodeID, Incarnation: l.Incarnation, InstanceID: l.InstanceID})
			if err != nil || cancel.ID != l.ID || cancel.SettledAt == nil || cancel.Reserved != 0 {
				t.Fatalf("deleted physical settlement: %+v %v", cancel, err)
			}
			b, err := store.FinancialComputeBalance(t.Context(), p, time.Now())
			if err != nil || b.CommittedMillicents <= 0 || b.ReservedMillicents != 0 {
				t.Fatalf("deletion reset job spending: %+v %v", b, err)
			}
		})
	}
}
