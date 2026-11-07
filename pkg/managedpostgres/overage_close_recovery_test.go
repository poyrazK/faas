// adr: 20261101143527 — existing reservations outlive new-usage close.
package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPendingOverageOutlivesCloseWindow(t *testing.T) {
	for _, offset := range []time.Duration{-time.Nanosecond, 0} {
		t.Run(offset.String(), func(t *testing.T) {
			month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
			closeAt := month.AddDate(0, 1, 0).Add(api.ManagedPostgresOverageCloseWindow)
			f := newOverageFixture(t, closeAt.Add(-time.Minute))
			f.reachAllowance(t, "acct", month, true)
			if err := f.store.SetOverage(t.Context(), "acct", true, f.now); err != nil {
				t.Fatal(err)
			}
			f.store.used["acct2026-10"] = testAllowance + 3600
			f.lostResponse = true
			if _, err := f.biller(t, true, month).Deliver(t.Context()); err == nil || len(f.published) != 1 {
				t.Fatalf("ambiguous delivery setup: published=%d err=%v", len(f.published), err)
			}
			held, found, err := f.store.OveragePending(t.Context(), "polar", "acct", month)
			if err != nil || !found {
				t.Fatalf("no durable pending: %t %v", found, err)
			}
			// A one-minute outage across close is enough; there is no opt-out.
			// Normal subject discovery still sees the durable reservation.
			f.now = closeAt.Add(offset)
			summary, err := f.biller(t, true, month).Deliver(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			_, pending, err := f.store.OveragePending(t.Context(), "polar", "acct", month)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("retry at %s: publish calls=%d, delivered steps=%d, old reservation still pending=%t", f.now.Format(time.RFC3339Nano), len(f.published), summary.Delivered, pending)
			if len(f.published) != 2 || pending || f.published[1] != held.Step {
				t.Fatal("an already-sent charge was abandoned at the new-usage close boundary")
			}
		})
	}
}

func TestPendingOveragePagesPastFailuresAndPreservesOtherProviders(t *testing.T) {
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, month.AddDate(0, 3, 0))
	var first OveragePendingStep
	for i := 0; i < 2*overageRecoveryPageSize+1; i++ {
		subject := fmt.Sprintf("acct-%04d", i)
		p := OveragePendingStep{Provider: "polar", Live: true, Step: OverageChargeStep{SubjectID: subject, PeriodStart: month,
			At: month.Add(time.Hour), AllowanceSeconds: testAllowance, UsedUnitSeconds: testAllowance + 3600, BilledUnitSeconds: 3600, TargetMillicents: hour}}
		if ok, err := f.store.ReserveOverage(t.Context(), p); err != nil || !ok {
			t.Fatal("reserve", err)
		}
		if i == 0 {
			first = p
		}
	}
	other := first
	other.Provider = "another"
	if _, err := f.store.ReserveOverage(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	future := first
	future.Step.PeriodStart, future.Step.At = month.AddDate(0, 4, 0), month.AddDate(0, 4, 0).Add(time.Hour)
	if _, err := f.store.ReserveOverage(t.Context(), future); err != nil {
		t.Fatal(err)
	}
	biller := f.biller(t, true, month.AddDate(0, 3, 0)) // effective-from does not abandon reservations.
	calls := 0
	biller.options.Publish = func(_ context.Context, step OverageChargeStep) error {
		calls++
		if step.SubjectID == first.Step.SubjectID {
			return ErrUnavailable
		}
		return nil
	}
	biller.options.Allowance = func(context.Context, string, time.Time) (CapLimits, error) { return CapLimits{}, ErrUnavailable }
	summary, err := biller.Deliver(t.Context())
	if !errors.Is(err, ErrUnavailable) || summary.Failed != 1 || summary.Delivered != 2*overageRecoveryPageSize || calls != 2*overageRecoveryPageSize+1 {
		t.Fatalf("old failures starved later recovery: summary=%+v calls=%d err=%v", summary, calls, err)
	}
	for _, p := range []OveragePendingStep{first, other, future} {
		if _, found, err := f.store.OveragePending(t.Context(), p.Provider, p.Step.SubjectID, p.Step.PeriodStart); err != nil || !found {
			t.Fatal("pending was lost", found, err)
		}
	}
}

func TestClosedLiveOverageCannotCompleteInShadow(t *testing.T) {
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, month.AddDate(0, 3, 0))
	p := OveragePendingStep{Provider: "polar", Live: true, Step: OverageChargeStep{SubjectID: "acct", PeriodStart: month, At: month.Add(time.Hour), TargetMillicents: hour}}
	if _, err := f.store.ReserveOverage(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if summary, err := f.biller(t, false, month).Deliver(t.Context()); err == nil || summary.Failed != 1 || summary.Shadowed != 0 {
		t.Fatalf("live step completed in shadow: %+v %v", summary, err)
	}
	if _, found, _ := f.store.OveragePending(t.Context(), "polar", "acct", month); !found {
		t.Fatal("live reservation disappeared")
	}
}
