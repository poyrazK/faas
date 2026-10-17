package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedInvoiceDirect injects a row into the MemStore's invoice map
// without going through the (PR-B) UpsertInvoice path. Mirrors the
// pgstore_invoices_test.go fixture so the e2e and the handler test
// stay aligned. The store contract is "list returns the rows" —
// who wrote them is the test's responsibility until PR B ships.
func seedInvoiceDirect(t *testing.T, store *state.MemStore, accountID, provider, providerInvoiceID string, periodEnd time.Time, totalCents int64) {
	t.Helper()
	inv := state.Invoice{
		ID:                "inv-" + providerInvoiceID,
		AccountID:         accountID,
		Provider:          provider,
		ProviderInvoiceID: providerInvoiceID,
		Status:            "paid",
		PeriodStart:       periodEnd.AddDate(0, 0, -30),
		PeriodEnd:         periodEnd,
		TotalCents:        totalCents,
		Currency:          "usd",
		PDFAvailable:      true,
	}
	// MemStore exposes invoices via the unexported `invoices` field;
	// reach it via the same package boundary the pgstore tests use.
	memSeedInvoice(store, inv)
}

func TestListInvoices_HappyPath_EmptyHistory(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	rec := e.Do(t, http.MethodGet, "/v1/invoices", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.InvoiceListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("expected empty items, got %d", len(out.Items))
	}
	if out.NextBefore != "" {
		t.Fatalf("expected empty cursor, got %q", out.NextBefore)
	}
}

func TestListInvoices_BadMonth(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	rec := e.Do(t, http.MethodGet, "/v1/invoices?month=garbage", nil, nil)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}

func TestListInvoices_BadCursor(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	rec := e.Do(t, http.MethodGet, "/v1/invoices?before=not-a-timestamp", nil, nil)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}

func TestListInvoices_BadLimit(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	rec := e.Do(t, http.MethodGet, "/v1/invoices?limit=999", nil, nil)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}

func TestListInvoices_CrossAccountIsolation(t *testing.T) {
	// Two accounts, same handler: alice's row must not appear in bob's
	// response. The store filter is account_id-only; the test pins
	// the contract that GET /v1/invoices never returns another
	// account's rows.
	alice := apidserver.Setup(t, api.PlanHobby)
	bob := apidserver.Setup(t, api.PlanHobby)

	july := time.Date(2026, 7, 31, 23, 59, 59, 0, time.UTC)
	seedInvoiceDirect(t, (*alice.Store()), (*alice.Acct()).ID, "stripe", "in_alice", july, 900)

	rec := alice.Do(t, http.MethodGet, "/v1/invoices", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("alice status %d", rec.Code)
	}
	var aliceResp api.InvoiceListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &aliceResp); err != nil {
		t.Fatalf("unmarshal alice: %v", err)
	}
	if len(aliceResp.Items) != 1 {
		t.Fatalf("alice expected 1 invoice, got %d", len(aliceResp.Items))
	}
	if aliceResp.Items[0].ProviderInvoiceID != "in_alice" {
		t.Fatalf("alice got wrong row: %q", aliceResp.Items[0].ProviderInvoiceID)
	}

	rec = bob.Do(t, http.MethodGet, "/v1/invoices", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("bob status %d", rec.Code)
	}
	var bobResp api.InvoiceListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &bobResp); err != nil {
		t.Fatalf("unmarshal bob: %v", err)
	}
	if len(bobResp.Items) != 0 {
		t.Fatalf("bob expected 0 invoices, got %d (cross-account leak)", len(bobResp.Items))
	}
}

func TestListInvoices_MonthFilter(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	july := time.Date(2026, 7, 31, 23, 59, 59, 0, time.UTC)
	aug := time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)
	seedInvoiceDirect(t, (*e.Store()), (*e.Acct()).ID, "stripe", "in_jul", july, 900)
	seedInvoiceDirect(t, (*e.Store()), (*e.Acct()).ID, "stripe", "in_aug", aug, 900)

	rec := e.Do(t, http.MethodGet, "/v1/invoices?month=2026-07", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.InvoiceListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ProviderInvoiceID != "in_jul" {
		t.Fatalf("month filter broken: got %+v", out.Items)
	}
}
