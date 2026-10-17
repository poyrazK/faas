package financialtest

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func newEgressFixture(t *testing.T, store computeTestStore, computeGrant, byteGrant, limit int64) accountComputeFixture {
	t.Helper()
	f := newAccountComputeFixture(t, store, computeGrant, limit, false)
	month := financial.ComputeMonth(time.Now())
	_, err := store.PutFinancialPriceSnapshot(t.Context(), state.FinancialPriceSnapshot{AccountID: f.account.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), Plan: api.PlanHobby, EffectiveFrom: month, DeliveryMode: "live", Price: financial.Price{Version: "egress-first", Meter: "egress", Currency: "EUR", Unit: "interface_bytes", UnitQuantity: 1024 * 1024, MillicentsPerUnit: 1, IncludedQuantity: byteGrant}})
	if err != nil {
		t.Fatal(err)
	}
	for at := f.account.CreatedAt.Truncate(time.Minute); !at.After(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
		if err := store.RecordFinancialSamplingWindow(t.Context(), at, true, true); err != nil {
			t.Fatal(err)
		}
	}
	f.policy.Spec.Meters = []string{"compute", "egress"}
	f.policy, err = store.CreateFinancialBudget(t.Context(), f.account.ID, uuid.NewString(), "test", f.policy.Spec)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFinancialEgressFunding(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store computeTestStore = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = financialPostgres(t)
			}
			t.Run("compute_and_bytes_share_one_paid_limit", func(t *testing.T) {
				f := newEgressFixture(t, store, 0, 0, 49)
				l, err := store.AcquireFinancialCompute(t.Context(), f.request(0))
				if err != nil {
					t.Fatal(err)
				}
				b := f.balance(t)
				if !l.EgressFunded || l.EgressSequence != 1 || l.EgressGrant < api.FinancialEgressMinimumCreditBytes || l.EgressGrant >= api.FinancialEgressCreditBytes || b.Egress == nil || b.ReservedMillicents != 49 || b.Egress.ReservedMillicents <= 0 {
					t.Fatalf("shared headroom: lease=%+v balance=%+v", l, b)
				}
				if b.ReservedMillicents <= b.Egress.ReservedMillicents {
					t.Fatal("compute was omitted from shared limit")
				}
				f.settle(t, l)
			})
			t.Run("contention_and_replay_do_not_duplicate_allowances", func(t *testing.T) {
				f := newEgressFixture(t, store, 153600, api.FinancialEgressCreditBytes, 0)
				var mu sync.Mutex
				var wins []financial.ComputeLease
				var requests []financial.ComputeRequest
				for i := range 12 {
					requests = append(requests, f.request(i))
				}
				var wg sync.WaitGroup
				for _, r := range requests {
					wg.Go(func() {
						l, err := store.AcquireFinancialCompute(t.Context(), r)
						mu.Lock()
						defer mu.Unlock()
						if err == nil {
							wins = append(wins, l)
						} else if !errors.Is(err, financial.ErrComputeBudget) {
							t.Errorf("acquire: %v", err)
						}
					})
				}
				wg.Wait()
				if len(wins) != 1 {
					t.Fatalf("winners=%d", len(wins))
				}
				before := f.balance(t)
				if before.Egress.ReservedBytes != api.FinancialEgressCreditBytes || before.Egress.AllowanceReserved != api.FinancialEgressCreditBytes || before.AllowanceReserved != 153600 || !before.Held {
					t.Fatalf("single grant: %+v", before)
				}
				var original financial.ComputeRequest
				for _, r := range requests {
					if r.InstanceID == wins[0].InstanceID {
						original = r
					}
				}
				again, err := store.AcquireFinancialCompute(t.Context(), original)
				if err != nil || again.EgressGranted != wins[0].EgressGranted || f.balance(t).Egress.ReservedBytes != before.Egress.ReservedBytes {
					t.Fatalf("replay minted credit: %+v %v", again, err)
				}
				f.settle(t, wins[0])
				after := f.balance(t)
				if after.Egress.ReservedBytes != 0 || after.Egress.CommittedBytes != api.FinancialEgressCreditBytes || after.Egress.AllowanceCommitted != api.FinancialEgressCreditBytes || !after.Held {
					t.Fatalf("conservative settlement: %+v", after)
				}
			})
			t.Run("refill_is_fenced_and_unused_credits_never_refund", func(t *testing.T) {
				f := newEgressFixture(t, store, 10000000, 0, 100)
				l, err := store.AcquireFinancialCompute(t.Context(), f.request(0))
				if err != nil {
					t.Fatal(err)
				}
				r := jobComputeMutation(l)
				l, err = store.RenewFinancialCompute(t.Context(), r)
				if err != nil {
					t.Fatal(err)
				}
				if l.EgressSequence != 1 || l.EgressGranted != api.FinancialEgressCreditBytes || l.EgressGrant != api.FinancialEgressCreditBytes {
					t.Fatal("time renewal changed byte credit")
				}
				r = jobComputeMutation(l)
				r.EgressRefill = true
				l, err = store.RenewFinancialCompute(t.Context(), r)
				if err != nil {
					t.Fatal(err)
				}
				again, err := store.RenewFinancialCompute(t.Context(), r)
				if err != nil || again.EgressSequence != 2 || again.EgressGranted != 2*api.FinancialEgressCreditBytes {
					t.Fatalf("refill replay: %+v %v", again, err)
				}
				r.EgressRefill = false
				if _, err := store.RenewFinancialCompute(t.Context(), r); !errors.Is(err, financial.ErrComputeFence) {
					t.Fatalf("changed replay bit: %v", err)
				}
				if _, err := store.DeleteFinancialBudget(t.Context(), f.account.ID, f.policy.ID, f.policy.Revision, "test"); err != nil {
					t.Fatal(err)
				}
				f.settle(t, l)
				b := f.balance(t)
				if b.Egress.ReservedBytes != 0 || b.Egress.CommittedBytes != 2*api.FinancialEgressCreditBytes || b.Egress.CommittedMillicents != 16 {
					t.Fatalf("policy deletion refunded unused credits: %+v", b)
				}
			})
			t.Run("higher_price_cannot_reprice_funded_bytes", func(t *testing.T) {
				f := newEgressFixture(t, store, 1000000, 0, 100)
				month := financial.ComputeMonth(time.Now())
				_, err := store.PutFinancialPriceSnapshot(t.Context(), state.FinancialPriceSnapshot{AccountID: f.account.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), Plan: api.PlanHobby, EffectiveFrom: time.Now().UTC().Truncate(time.Minute), DeliveryMode: "live", Price: financial.Price{Version: "egress-price-increase", Meter: "egress", Currency: "EUR", Unit: "interface_bytes", UnitQuantity: 1024 * 1024, MillicentsPerUnit: 2}})
				if !errors.Is(err, financial.ErrComputeUnavailable) {
					t.Fatalf("above-ceiling price accepted: %v", err)
				}
			})
			t.Run("independent_job_uses_same_byte_pool", func(t *testing.T) {
				f := newEgressFixture(t, store, 1000000, api.FinancialEgressCreditBytes, 0)
				job, run := computeJob(t, f, "batch")
				r := claimComputeJob(t, f, job, run, 0)
				l, err := store.AcquireFinancialCompute(t.Context(), r)
				if err != nil {
					t.Fatal(err)
				}
				if !l.EgressFunded || l.JobID != job.ID || l.EgressGrant != api.FinancialEgressCreditBytes {
					t.Fatalf("unfunded job: %+v", l)
				}
				denied, err := store.AcquireFinancialCompute(t.Context(), f.request(0))
				if !errors.Is(err, financial.ErrComputeBudget) || !denied.Denied || denied.EgressGranted != 0 {
					t.Fatalf("app escaped spent job bytes: %+v %v", denied, err)
				}
				if _, err := store.SettleFinancialCompute(t.Context(), jobComputeMutation(l)); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}
