// Issue #279 PR A — POST /v1/admin/accounts/{id}/credits — handler
// tests. Pins the contract:
//
//  1. Two-layer auth: scope (admin-only) on the route + email
//     allowlist (FAAS_ADMIN_EMAILS) on the handler. Either layer
//     missing → 403.
//  2. Validation: cents > 0, reason 3..500 chars, target UUID
//     well-formed. Otherwise → 400 CodeValidation.
//  3. Persistence: a row in account_credits + a row in credit_ledger
//     + an audit event "credit.issued".
//  4. Idempotency: replaying the same Idempotency-Key returns the
//     same credit_id and never writes a duplicate ledger row.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// newIssueCreditEnv is the testEnv twin for the admin credit route.
// It wires a single admin allowlist entry (the test operator), mints
// a bearer with the requested scopes, and returns the env plus the
// rt so the test can build its own requests with explicit bodies +
// Idempotency-Key headers.
func newIssueCreditEnv(t *testing.T, scopes []string, adminEmail, callerEmail string) testEnv {
	t.Helper()
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid_credits_test")
	acct, err := store.CreateAccount(context.Background(), callerEmail, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "credits-test", scopes); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), ops)
	srv.WithAdminAllowlist(adminEmail)
	return testEnv{h: srv.handler(), s: srv, store: store, key: pt, acct: acct, ops: ops}
}

func TestIssueCredit_HappyPath(t *testing.T) {
	e := newIssueCreditEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	target, err := e.store.CreateAccount(context.Background(), "alvo@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}
	body := bytes.NewBufferString(`{"cents": 500, "reason": "goodwill for outage"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/accounts/"+target.ID+"/credits", body)
	e.addAdminSession(t, req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "test-credits-1")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var resp api.AccountCreditResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body)
	}
	if resp.AccountID != target.ID {
		t.Errorf("AccountID = %q, want %q", resp.AccountID, target.ID)
	}
	if resp.CentsRemaining != 500 {
		t.Errorf("CentsRemaining = %d, want 500", resp.CentsRemaining)
	}
	if resp.Reason != "goodwill for outage" {
		t.Errorf("Reason = %q", resp.Reason)
	}
	if _, err := uuid.Parse(resp.ID); err != nil {
		t.Errorf("ID = %q is not a UUID", resp.ID)
	}
	// Exactly one credit row + one ledger row.
	rows, err := e.store.ListAccountCredits(context.Background(), target.ID, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("account_credits rows = %d, want 1", len(rows))
	}
	// Audit row — the auditor's Emit is best-effort, so a happy-path
	// test must inspect the events table to know the row actually
	// landed (and not just that Emit returned without panicking).
	events, err := e.store.ListEvents(context.Background(), target.ID, 50)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var found *state.Event
	for i := range events {
		if events[i].Kind == "credit.issued" {
			found = &events[i]
			break
		}
	}
	found = mustEvent(t, found, fmt.Sprintf("credit.issued audit row missing for account %s", target.ID))
	if found.Actor != "apid" {
		t.Errorf("audit Actor = %q, want apid", found.Actor)
	}
}

func TestIssueCredit_NonAdminScopeForbidden(t *testing.T) {
	e := newIssueCreditEnv(t, []string{api.ScopeDeployWrite}, "ops@example.com", "ops@example.com")
	target, err := e.store.CreateAccount(context.Background(), "alvo@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}
	rec := e.do(t, http.MethodPost, "/v1/admin/accounts/"+target.ID+"/credits",
		map[string]any{"cents": 500, "reason": "goodwill for outage"}, nil)
	assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
}

func TestIssueCredit_AdminScopeButEmailNotAllowed(t *testing.T) {
	// Admin scope, but the caller's email is NOT in FAAS_ADMIN_EMAILS.
	// adminAllows check must fire AFTER the requireScope middleware
	// (which passes), so the operator allowlist is the gate. The
	// allowlist miss returns the admin_required sentinel defined at
	// cmd/apid/compute_nodes.go:78.
	e := newIssueCreditEnv(t, api.ScopesAdminOnly, "allowed@example.com", "intruder@example.com")
	target, err := e.store.CreateAccount(context.Background(), "alvo@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("seed target: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/accounts/"+target.ID+"/credits",
		bytes.NewBufferString(`{"cents":500,"reason":"goodwill for outage"}`))
	rec := httptest.NewRecorder()
	e.s.issueCredit(rec, req, e.acct)
	assertProblem(t, rec, http.StatusForbidden, "admin_required")
}

func TestIssueCredit_RejectsBadReason(t *testing.T) {
	cases := []struct {
		name   string
		reason string
	}{
		{"empty", ""},
		{"too_short", "ab"},
		{"too_long", strings.Repeat("x", 501)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newIssueCreditEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
			target, err := e.store.CreateAccount(context.Background(), "alvo@example.com", api.PlanHobby)
			if err != nil {
				t.Fatalf("seed: %v", err)
			}
			rec := e.doAdmin(t, http.MethodPost, "/v1/admin/accounts/"+target.ID+"/credits",
				map[string]any{"cents": 500, "reason": tc.reason}, nil)
			assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
		})
	}
}

func TestIssueCredit_RejectsBadCents(t *testing.T) {
	cases := []struct {
		name  string
		cents int64
	}{
		{"zero", 0},
		{"negative", -100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newIssueCreditEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
			target, err := e.store.CreateAccount(context.Background(), "alvo@example.com", api.PlanHobby)
			if err != nil {
				t.Fatalf("seed: %v", err)
			}
			rec := e.doAdmin(t, http.MethodPost, "/v1/admin/accounts/"+target.ID+"/credits",
				map[string]any{"cents": tc.cents, "reason": "goodwill for outage"}, nil)
			assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
		})
	}
}

func TestIssueCredit_AccountNotFound(t *testing.T) {
	e := newIssueCreditEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	missingID := uuid.NewString()
	rec := e.doAdmin(t, http.MethodPost, "/v1/admin/accounts/"+missingID+"/credits",
		map[string]any{"cents": 500, "reason": "goodwill for outage"}, nil)
	assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
}

func TestIssueCredit_BadUUID(t *testing.T) {
	e := newIssueCreditEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	rec := e.doAdmin(t, http.MethodPost, "/v1/admin/accounts/not-a-uuid/credits",
		map[string]any{"cents": 500, "reason": "goodwill for outage"}, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}

func TestIssueCredit_Idempotent(t *testing.T) {
	e := newIssueCreditEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	target, err := e.store.CreateAccount(context.Background(), "alvo@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// First call.
	body := bytes.NewBufferString(`{"cents": 500, "reason": "goodwill for outage"}`)
	req1 := httptest.NewRequest(http.MethodPost, "/v1/admin/accounts/"+target.ID+"/credits", body)
	e.addAdminSession(t, req1)
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Idempotency-Key", "test-credits-idem")
	rec1 := httptest.NewRecorder()
	e.h.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first call: status=%d body=%s", rec1.Code, rec1.Body)
	}
	var resp1 api.AccountCreditResponse
	if err := json.Unmarshal(rec1.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("decode1: %v", err)
	}

	// Second call with the same Idempotency-Key + body.
	body2 := bytes.NewBufferString(`{"cents": 500, "reason": "goodwill for outage"}`)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/admin/accounts/"+target.ID+"/credits", body2)
	e.addAdminSession(t, req2)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Idempotency-Key", "test-credits-idem")
	rec2 := httptest.NewRecorder()
	e.h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("second call: status=%d body=%s", rec2.Code, rec2.Body)
	}
	var resp2 api.AccountCreditResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode2: %v", err)
	}
	if resp1.ID != resp2.ID {
		t.Errorf("idempotent replay: id changed from %q to %q", resp1.ID, resp2.ID)
	}

	// Exactly one credit row in the store.
	rows, err := e.store.ListAccountCredits(context.Background(), target.ID, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("account_credits rows = %d, want 1 (idempotent)", len(rows))
	}
}

// mustEvent is the SA5011 escape hatch for the credit.issued /
// audit-row lookups: ListEvents can legitimately return 0 rows
// of a given Kind, but we want a real event for assertions below.
// A helper that t.Fatal()s and returns the value lets staticcheck
// see the value is non-nil at the call site.
func mustEvent(t *testing.T, ev *state.Event, msg string) *state.Event {
	t.Helper()
	if ev == nil {
		t.Fatal(msg)
	}
	return ev
}
