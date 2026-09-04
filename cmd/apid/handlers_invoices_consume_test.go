// Issue #279 PR-C — POST /v1/invoices/{id}/consume-credits — handler
// tests. Pins the contract:
//
//  1. Two-layer auth: scope (admin-only) + email allowlist
//     (FAAS_ADMIN_EMAILS). Either layer missing → 403.
//  2. Validation: invoice id is a UUID. Otherwise → 400.
//  3. Reducer happy path: 200 with consumed_cents + per_credit; an
//     audit event "credit.consumed" lands on the events table.
//  4. Idempotency: a second call with a fresh Idempotency-Key
//     returns already_consumed_for_invoice=true and never writes
//     a duplicate ledger row.
//  5. Unknown invoice: 404 CodeNotFound.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// newConsumeEnv is the testEnv twin for the consume route. Mirrors
// newIssueCreditEnv — same MemStore, same admin allowlist. The
// caller is the operator account; the test plants usage_minutes +
// credits + invoice fixtures inside the helper body so each test
// can name its own target overage shape.
func newConsumeEnv(t *testing.T, scopes []string, adminEmail, callerEmail string) testEnv {
	t.Helper()
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid_consume_test")
	acct, err := store.CreateAccount(context.Background(), callerEmail, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "consume-test", scopes); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), ops)
	srv.WithAdminAllowlist(adminEmail)
	return testEnv{h: srv.handler(), store: store, key: pt, acct: acct, ops: ops}
}

func TestConsumeInvoice_HappyPath(t *testing.T) {
	e := newConsumeEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	target, err := e.store.CreateAccount(context.Background(), "alvo@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}
	// One credit of 250 cents — the reducer should drain it fully.
	credit, err := e.store.CreateAccountCredit(context.Background(), state.AccountCredit{
		AccountID:      target.ID,
		CentsRemaining: 250,
		Reason:         "goodwill",
	})
	if err != nil {
		t.Fatalf("credit: %v", err)
	}
	// Invoice with provider_invoice_id "in_xxx" and PeriodEnd covering
	// the usage we plant next. Overage target = 250 cents after the
	// Hobby plan's 50 included GB-hour allowance.
	inv := state.Invoice{
		ID:                uuid.NewString(),
		AccountID:         target.ID,
		Provider:          "stripe",
		ProviderInvoiceID: "in_consume_happy",
	}
	e.store.SeedInvoiceForTest(inv)
	if err := e.store.AppendUsage(context.Background(), target.ID, "app-1", "inst-1",
		// past minute so the all-usage compatibility path picks it up.
		// The reducer passes inv.PeriodStart = zero time, so all rows
		// land in the window.
		time.Now().UTC(), 300*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("usage: %v", err)
	}

	rec := e.do(t, http.MethodPost, "/v1/invoices/"+inv.ID+"/consume-credits", nil,
		map[string]string{"Idempotency-Key": "test-consume-happy"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var resp api.ConsumeInvoiceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body)
	}
	if resp.InvoiceID != inv.ID {
		t.Errorf("InvoiceID = %q, want %q", resp.InvoiceID, inv.ID)
	}
	if resp.ConsumedCents != 250 {
		t.Errorf("ConsumedCents = %d, want 250", resp.ConsumedCents)
	}
	if resp.AlreadyConsumedForInvoice {
		t.Errorf("AlreadyConsumedForInvoice = true on first call")
	}
	if len(resp.PerCredit) != 1 || resp.PerCredit[0].CreditID != credit.ID {
		t.Fatalf("PerCredit = %+v, want 1 row for credit %s", resp.PerCredit, credit.ID)
	}
	if resp.PerCredit[0].DeltaCents != -250 {
		t.Errorf("DeltaCents = %d, want -250", resp.PerCredit[0].DeltaCents)
	}
	if resp.PerCredit[0].NewBalance != 0 {
		t.Errorf("NewBalance = %d, want 0", resp.PerCredit[0].NewBalance)
	}

	// credit.consumed audit row landed.
	events, err := e.store.ListEvents(context.Background(), target.ID, 50)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var consumed *state.Event
	for i := range events {
		if events[i].Kind == "credit.consumed" {
			consumed = &events[i]
			break
		}
	}
	consumed = mustAuditEvent(t, consumed, fmt.Sprintf("credit.consumed audit row missing for account %s", target.ID))
	if consumed.Actor != "apid" {
		t.Errorf("audit Actor = %q, want apid", consumed.Actor)
	}
}

func TestConsumeInvoice_IdempotentReplay(t *testing.T) {
	e := newConsumeEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	target, err := e.store.CreateAccount(context.Background(), "alvo@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := e.store.CreateAccountCredit(context.Background(), state.AccountCredit{
		AccountID:      target.ID,
		CentsRemaining: 250,
		Reason:         "goodwill",
	}); err != nil {
		t.Fatalf("credit: %v", err)
	}
	inv := state.Invoice{
		ID:                uuid.NewString(),
		AccountID:         target.ID,
		Provider:          "stripe",
		ProviderInvoiceID: "in_consume_replay",
	}
	e.store.SeedInvoiceForTest(inv)
	if err := e.store.AppendUsage(context.Background(), target.ID, "app-1", "inst-1",
		time.Now().UTC(), 300*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("usage: %v", err)
	}

	// First call — full drain.
	rec1 := e.do(t, http.MethodPost, "/v1/invoices/"+inv.ID+"/consume-credits", nil,
		map[string]string{"Idempotency-Key": "test-consume-replay-1"})
	if rec1.Code != http.StatusOK {
		t.Fatalf("first call: status=%d body=%s", rec1.Code, rec1.Body)
	}

	// Replay with a fresh Idempotency-Key (so the idempotent middleware
	// doesn't short-circuit). The reducer-level dedupe must kick in:
	// already_consumed_for_invoice=true, no new ledger rows.
	rec2 := e.do(t, http.MethodPost, "/v1/invoices/"+inv.ID+"/consume-credits", nil,
		map[string]string{"Idempotency-Key": "test-consume-replay-2"})
	if rec2.Code != http.StatusOK {
		t.Fatalf("second call: status=%d body=%s", rec2.Code, rec2.Body)
	}
	var resp2 api.ConsumeInvoiceResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode2: %v", err)
	}
	if !resp2.AlreadyConsumedForInvoice {
		t.Errorf("AlreadyConsumedForInvoice = false, want true (reducer-level dedupe)")
	}
	if resp2.ConsumedCents != 250 {
		t.Errorf("ConsumedCents = %d, want 250 (same total)", resp2.ConsumedCents)
	}
	if len(resp2.PerCredit) != 0 {
		t.Errorf("PerCredit len = %d, want 0 on replay", len(resp2.PerCredit))
	}

	// Exactly 2 ledger rows: 1 issuance (create-credit records the
	// issuance delta; MemStore doesn't, see pgstore path) + 1
	// consumption. Actually MemStore's CreateAccountCredit does NOT
	// write a ledger row (only the issuance handler does), so we
	// expect 1 row total.
	ledger := e.store.ListCreditLedgerForTest(target.ID)
	if len(ledger) != 1 {
		t.Fatalf("ledger len = %d, want 1 (idempotent)", len(ledger))
	}
}

func TestConsumeInvoice_NonAdminScopeForbidden(t *testing.T) {
	e := newConsumeEnv(t, []string{api.ScopeDeployWrite}, "ops@example.com", "ops@example.com")
	invID := uuid.NewString()
	rec := e.do(t, http.MethodPost, "/v1/invoices/"+invID+"/consume-credits", nil, nil)
	assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
}

func TestConsumeInvoice_AdminScopeButEmailNotAllowed(t *testing.T) {
	e := newConsumeEnv(t, api.ScopesAdminOnly, "allowed@example.com", "intruder@example.com")
	invID := uuid.NewString()
	rec := e.do(t, http.MethodPost, "/v1/invoices/"+invID+"/consume-credits", nil, nil)
	assertProblem(t, rec, http.StatusForbidden, "admin_required")
}

func TestConsumeInvoice_BadUUID(t *testing.T) {
	e := newConsumeEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	rec := e.do(t, http.MethodPost, "/v1/invoices/not-a-uuid/consume-credits", nil, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}

func TestConsumeInvoice_UnknownInvoice(t *testing.T) {
	e := newConsumeEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	missingID := uuid.NewString()
	rec := e.do(t, http.MethodPost, "/v1/invoices/"+missingID+"/consume-credits", nil, nil)
	assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
}
