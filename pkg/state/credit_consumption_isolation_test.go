// spec: §10 — invoice retries and compensating credits remain idempotent.
// spec: §11 — credit mutations stay inside the owning account.
package state_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCreditConsumptionIsolation(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = pgStoreAccountCreditsWithPool(t)
			}
			t.Run("replay belongs to account", func(t *testing.T) { testCreditReplayAccountScope(t, store) })
			t.Run("provider scope", func(t *testing.T) { testCreditProviderScope(t, store) })
			t.Run("legacy provider unknown", func(t *testing.T) { testCreditUnqualifiedProvider(t, store) })
			t.Run("reversal belongs to account", func(t *testing.T) { testCreditReversalAccountScope(t, store) })
			t.Run("one compensation", func(t *testing.T) { testCreditReversalSingleCompensation(t, store) })
			t.Run("no-op balance", func(t *testing.T) { testCreditConsumptionNoOp(t, store) })
		})
	}
}

func TestInvoiceProviderKeyAmbiguity(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store, _, _ = pgStoreAccountCreditsWithPool(t)
			}
			ctx := context.Background()
			acct := creditAccount(t, store, "ambiguous-key@example.com", 0)
			for _, tc := range []struct{ invoiceID, chargeID string }{
				{"invoice-a", "shared-key"},
				{"shared-key", "charge-b"},
			} {
				if err := store.UpsertInvoice(ctx, state.Invoice{AccountID: acct.ID, Provider: "polar", ProviderInvoiceID: tc.invoiceID, ProviderChargeID: tc.chargeID, Status: "paid", Plan: api.PlanHobby, TotalCents: 100, AmountPaidCents: 100}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.GetInvoiceByProviderID(ctx, acct.ID, "polar", "shared-key"); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("ambiguous invoice/charge key = %v, want conflict", err)
			}
			if _, err := store.GetInvoiceByProviderID(ctx, acct.ID, "polar", ""); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("empty key = %v, want not found", err)
			}
			for _, tc := range []struct{ key, want string }{{"invoice-a", "invoice-a"}, {"charge-b", "shared-key"}} {
				inv, err := store.GetInvoiceByProviderID(ctx, acct.ID, "polar", tc.key)
				if err != nil || inv.ProviderInvoiceID != tc.want {
					t.Errorf("lookup %q = (%+v, %v), want invoice %q", tc.key, inv, err, tc.want)
				}
			}
		})
	}
}

func testCreditUnqualifiedProvider(t *testing.T, store state.Store) {
	ctx := context.Background()
	acct := creditAccount(t, store, "legacy-provider@example.com", 140)
	credits, err := store.ListAccountCredits(ctx, acct.ID, false)
	if err != nil || len(credits) != 1 {
		t.Fatalf("credits = (%v, %v)", credits, err)
	}
	invoiceID := "legacy-unresolved-invoice"
	// This balance and audit row model a legacy 60-cent debit whose provider
	// could not be identified by the migration. Do not guess on later retries.
	if err := store.CreateCreditLedgerEntry(ctx, state.CreditLedgerEntry{AccountID: acct.ID, CreditID: credits[0].ID, DeltaCents: -60, Reason: "legacy consumption", Actor: "test", ProviderInvoiceID: &invoiceID}); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"stripe", "paddle", "polar"} {
		params := state.ConsumeAccountCreditParams{AccountID: acct.ID, TargetCents: 60, Provider: provider, ProviderInvoiceID: invoiceID, Reason: "test", Actor: "test"}
		if _, err := store.ConsumeAccountCredit(ctx, params); !errors.Is(err, state.ErrConflict) {
			t.Errorf("%s unresolved replay = %v, want conflict", provider, err)
		}
		if err := store.UpsertInvoice(ctx, state.Invoice{AccountID: acct.ID, Provider: provider, ProviderInvoiceID: invoiceID, Plan: api.PlanHobby, Status: "paid", TotalCents: 200, AmountPaidCents: 200}); err != nil {
			t.Fatal(err)
		}
		inv, err := store.GetInvoiceByProviderID(ctx, acct.ID, provider, invoiceID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RecordInvoiceRefund(ctx, state.InvoiceRefund{InvoiceID: inv.ID, ProviderRefundID: "legacy-refund", IdempotencyKey: "legacy-refund", AmountCents: 60, Source: "credit", Status: "failed"}); !errors.Is(err, state.ErrConflict) {
			t.Errorf("%s unresolved refund = %v, want conflict", provider, err)
		}
	}
	requireCreditBalance(t, store, acct.ID, 140)
}

func testCreditProviderScope(t *testing.T, store state.Store) {
	ctx := context.Background()
	acct := creditAccount(t, store, "provider-scope@example.com", 200)
	params := state.ConsumeAccountCreditParams{AccountID: acct.ID, TargetCents: 60, Provider: "stripe", ProviderInvoiceID: "provider-shared-id", Reason: "test", Actor: "test"}
	if _, err := store.ConsumeAccountCredit(ctx, params); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertInvoice(ctx, state.Invoice{AccountID: acct.ID, Provider: "polar", ProviderInvoiceID: params.ProviderInvoiceID, Plan: api.PlanHobby, Status: "paid", TotalCents: 200, AmountPaidCents: 200}); err != nil {
		t.Fatal(err)
	}
	inv, err := store.GetInvoiceByProviderID(ctx, acct.ID, "polar", params.ProviderInvoiceID)
	if err != nil {
		t.Fatal(err)
	}
	refund := state.InvoiceRefund{InvoiceID: inv.ID, ProviderRefundID: "provider-scope-refund", IdempotencyKey: "provider-scope-key", AmountCents: 60, Source: "credit", Status: "failed"}
	if err := store.RecordInvoiceRefund(ctx, refund); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("refund of another provider's consumption = %v, want conflict", err)
	}
	params.Provider, params.TargetCents = "polar", 80
	result, err := store.ConsumeAccountCredit(ctx, params)
	if err != nil || result.ConsumedCents != 80 || result.AlreadyConsumedForInvoice {
		t.Fatalf("second provider consumption = (%+v, %v), want independent debit of 80", result, err)
	}
	requireCreditBalance(t, store, acct.ID, 60)
	refund.AmountCents = 80
	for range 2 {
		if err := store.RecordInvoiceRefund(ctx, refund); err != nil {
			t.Fatal(err)
		}
	}
	requireCreditBalance(t, store, acct.ID, 140)
	for _, tc := range []struct {
		provider string
		consumed int64
	}{{"stripe", 60}, {"polar", 0}} {
		params.Provider = tc.provider
		result, err := store.ConsumeAccountCredit(ctx, params)
		if err != nil || result.ConsumedCents != tc.consumed || !result.AlreadyConsumedForInvoice {
			t.Errorf("%s replay = (%+v, %v), want %d and no further debit", tc.provider, result, err, tc.consumed)
		}
	}
	requireCreditBalance(t, store, acct.ID, 140)
}

func creditAccount(t *testing.T, store state.Store, email string, cents int64) state.Account {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, email, api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	if cents > 0 {
		if _, err := store.CreateAccountCredit(ctx, state.AccountCredit{AccountID: acct.ID, CentsRemaining: cents, Reason: "test credit"}); err != nil {
			t.Fatal(err)
		}
	}
	return acct
}

func requireCreditBalance(t *testing.T, store state.Store, accountID string, want int64) {
	t.Helper()
	credits, err := store.ListAccountCredits(context.Background(), accountID, false)
	if err != nil {
		t.Fatal(err)
	}
	var got int64
	for _, credit := range credits {
		got += credit.CentsRemaining
	}
	if got != want {
		t.Fatalf("credit balance = %d, want %d", got, want)
	}
}

func testCreditReplayAccountScope(t *testing.T, store state.Store) {
	ctx := context.Background()
	a := creditAccount(t, store, "replay-a@example.com", 150)
	b := creditAccount(t, store, "replay-b@example.com", 200)
	for attempt := 0; attempt < 2; attempt++ {
		for _, tc := range []struct {
			account         state.Account
			amount, balance int64
		}{
			{a, 60, 90}, {b, 80, 120},
		} {
			result, err := store.ConsumeAccountCredit(ctx, state.ConsumeAccountCreditParams{
				AccountID: tc.account.ID, TargetCents: tc.amount, Provider: "polar",
				ProviderInvoiceID: "shared-invoice", Reason: "test", Actor: "test",
			})
			if err != nil || result.ConsumedCents != tc.amount || result.RemainingCreditsCents != tc.balance || result.AlreadyConsumedForInvoice != (attempt > 0) {
				t.Errorf("account %s attempt %d = (%+v, %v); want own consumption %d, balance %d", tc.account.ID, attempt, result, err, tc.amount, tc.balance)
			}
		}
	}
	requireCreditBalance(t, store, a.ID, 90)
	requireCreditBalance(t, store, b.ID, 120)
}

func testCreditReversalAccountScope(t *testing.T, store state.Store) {
	ctx := context.Background()
	a := creditAccount(t, store, "reverse-a@example.com", 150)
	b := creditAccount(t, store, "reverse-b@example.com", 200)
	params := state.ConsumeAccountCreditParams{AccountID: a.ID, TargetCents: 60, Provider: "polar", ProviderInvoiceID: "shared-refund-invoice", Reason: "test", Actor: "test"}
	if _, err := store.ConsumeAccountCredit(ctx, params); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertInvoice(ctx, state.Invoice{AccountID: b.ID, Provider: "polar", ProviderInvoiceID: params.ProviderInvoiceID, Plan: api.PlanHobby, Status: "paid", TotalCents: 200, AmountPaidCents: 200}); err != nil {
		t.Fatal(err)
	}
	inv, err := store.GetInvoiceByProviderID(ctx, b.ID, "polar", params.ProviderInvoiceID)
	if err != nil {
		t.Fatal(err)
	}
	refund := state.InvoiceRefund{InvoiceID: inv.ID, ProviderRefundID: "refund-other-account", IdempotencyKey: "key-other-account", AmountCents: 60, Source: "credit", Status: "failed"}
	if err := store.RecordInvoiceRefund(ctx, refund); !errors.Is(err, state.ErrConflict) {
		t.Errorf("reversal without own consumption = %v, want conflict", err)
	}
	requireCreditBalance(t, store, a.ID, 90)
	requireCreditBalance(t, store, b.ID, 200)
	params.AccountID, params.TargetCents = b.ID, 80
	if result, err := store.ConsumeAccountCredit(ctx, params); err != nil || result.ConsumedCents != 80 || result.AlreadyConsumedForInvoice {
		t.Fatalf("own consumption = (%+v, %v)", result, err)
	}
	refund.ProviderRefundID, refund.IdempotencyKey, refund.AmountCents = "refund-own-account", "key-own-account", 80
	refund.AmountCents--
	if err := store.RecordInvoiceRefund(ctx, refund); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("mismatched reversal = %v, want conflict", err)
	}
	requireCreditBalance(t, store, b.ID, 120)
	refund.AmountCents++
	for range 2 {
		if err := store.RecordInvoiceRefund(ctx, refund); err != nil {
			t.Fatal(err)
		}
	}
	requireCreditBalance(t, store, a.ID, 90)
	requireCreditBalance(t, store, b.ID, 200)
	result, err := store.ConsumeAccountCredit(ctx, params)
	if err != nil || result.ConsumedCents != 0 || !result.AlreadyConsumedForInvoice {
		t.Fatalf("replay after reversal = (%+v, %v), want no second debit", result, err)
	}
}

func testCreditReversalSingleCompensation(t *testing.T, store state.Store) {
	ctx := context.Background()
	acct := creditAccount(t, store, "single-compensation@example.com", 100)
	if _, err := store.CreateAccountCredit(ctx, state.AccountCredit{AccountID: acct.ID, CentsRemaining: 100, Reason: "second credit"}); err != nil {
		t.Fatal(err)
	}
	const providerInvoiceID = "single-compensation-invoice"
	if err := store.UpsertInvoice(ctx, state.Invoice{AccountID: acct.ID, Provider: "polar", ProviderInvoiceID: providerInvoiceID, Plan: api.PlanHobby, Status: "paid", TotalCents: 200, AmountPaidCents: 200}); err != nil {
		t.Fatal(err)
	}
	inv, err := store.GetInvoiceByProviderID(ctx, acct.ID, "polar", providerInvoiceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeAccountCredit(ctx, state.ConsumeAccountCreditParams{AccountID: acct.ID, Provider: "polar", ProviderInvoiceID: providerInvoiceID, TargetCents: 170, Actor: "test", Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"refund-a", "refund-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- store.RecordInvoiceRefund(ctx, state.InvoiceRefund{InvoiceID: inv.ID, ProviderRefundID: id, IdempotencyKey: id, AmountCents: 170, Source: "credit", Status: "failed"})
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var applied, conflicts int
	for err := range results {
		switch {
		case err == nil:
			applied++
		case errors.Is(err, state.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected reversal error: %v", err)
		}
	}
	if applied != 1 || conflicts != 1 {
		t.Errorf("applied=%d conflicts=%d, want exactly one compensation", applied, conflicts)
	}
	requireCreditBalance(t, store, acct.ID, 200)
}

func testCreditConsumptionNoOp(t *testing.T, store state.Store) {
	ctx := context.Background()
	acct := creditAccount(t, store, "noop@example.com", 125)
	params := state.ConsumeAccountCreditParams{AccountID: acct.ID, ProviderInvoiceID: "noop-invoice", Provider: "polar", Actor: "test", Reason: "test"}
	result, err := store.ConsumeAccountCredit(ctx, params)
	if err != nil || result.ConsumedCents != 0 || result.RemainingCreditsCents != 125 || result.AlreadyConsumedForInvoice || len(result.PerCredit) != 0 {
		t.Errorf("zero target = (%+v, %v), want unchanged 125-cent balance", result, err)
	}
	params.TargetCents = -1
	if _, err := store.ConsumeAccountCredit(ctx, params); err == nil {
		t.Error("negative consumption target accepted")
	}
	requireCreditBalance(t, store, acct.ID, 125)
	params.TargetCents = 25
	result, err = store.ConsumeAccountCredit(ctx, params)
	if err != nil || result.ConsumedCents != 25 || result.RemainingCreditsCents != 100 || result.AlreadyConsumedForInvoice {
		t.Fatalf("positive target after no-op = (%+v, %v), want fresh consumption", result, err)
	}
	for _, invalid := range []state.ConsumeAccountCreditParams{
		{AccountID: acct.ID, ProviderInvoiceID: params.ProviderInvoiceID, TargetCents: -1},
		{AccountID: " ", ProviderInvoiceID: params.ProviderInvoiceID},
		{AccountID: acct.ID, ProviderInvoiceID: " "},
		{AccountID: acct.ID, ProviderInvoiceID: "missing-provider", TargetCents: 10},
		{AccountID: acct.ID, Provider: "unknown", ProviderInvoiceID: "bad-provider", TargetCents: 10},
	} {
		if _, err := store.ConsumeAccountCredit(ctx, invalid); err == nil {
			t.Errorf("invalid request accepted: %+v", invalid)
		}
	}
	params.TargetCents = 0
	result, err = store.ConsumeAccountCredit(ctx, params)
	if err != nil || result.ConsumedCents != 25 || result.RemainingCreditsCents != 100 || !result.AlreadyConsumedForInvoice {
		t.Fatalf("zero target after consumption = (%+v, %v), want original receipt", result, err)
	}
	empty := creditAccount(t, store, "empty@example.com", 0)
	params.AccountID, params.ProviderInvoiceID, params.TargetCents = empty.ID, "empty-invoice", 50
	result, err = store.ConsumeAccountCredit(ctx, params)
	if err != nil || result.ConsumedCents != 0 || result.AlreadyConsumedForInvoice {
		t.Errorf("no available credit = (%+v, %v), want fresh no-op", result, err)
	}
}
