package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/focus"
)

// adr:381
func TestFOCUSSignedInvoiceIngestion(t *testing.T) {
	e := setup(t, api.PlanPro)
	var logs bytes.Buffer
	e.s.log = slog.New(slog.NewTextHandler(&logs, nil))
	e.s.stripeWebhookSecret = stripeWebhookSecretForTest
	e.s.WithBillingProvider(&consumeRefundProvider{})
	if err := e.store.UpdateAccountProviderCustomerID(context.Background(), e.acct.ID, "cus_test_123"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../pkg/billing/testdata/invoices/stripe.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte("cus_invoice"), []byte("cus_test_123"))
	send := func(body []byte) {
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", bytes.NewReader(body))
		req.Header.Set("Stripe-Signature", stripe.SignForTest(body, stripeWebhookSecretForTest, time.Now()))
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("webhook status=%d body=%s logs=%s", rec.Code, rec.Body, logs.String())
		}
	}
	send(raw)
	first, err := e.store.GetInvoiceByProviderID(context.Background(), e.acct.ID, "stripe", "in_invoice")
	if err != nil {
		t.Fatal(err)
	}
	send(raw) // the same signed event is replayed before export
	stored, err := e.store.GetInvoiceByID(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Details.Lines.Items[0] != first.Details.Lines.Items[0] {
		t.Fatal("delivery replay mutated line history")
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "focus-export", api.ScopesUsageReadSurface); err != nil {
		t.Fatal(err)
	}
	e.key = key
	rec := e.do(t, http.MethodGet, "/v1/billing/focus?month=2026-09&format=csv", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", rec.Code, rec.Body)
	}
	rows, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil || len(rows) != 5 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if rows[1][5] != "Purchase" || rows[1][0] != "9.00" || rows[3][5] != "Usage" || rows[3][0] != "1.00" {
		t.Fatalf("provider line classification missing: %+v", rows)
	}
	if rows[1][13] != "Gregale invoice business" || rows[1][16] != "Due by 2026-10-30T00:00:00Z" {
		t.Fatal("invoice terms or issuer missing")
	}
	rec = e.do(t, http.MethodGet, "/v1/billing/focus?month=2026-09&format=metadata", nil, nil)
	var m focus.Metadata
	if json.Unmarshal(rec.Body.Bytes(), &m) != nil || m.Projection.SourceCoverage.DetailedInvoices != 1 || len(m.Projection.MissingRequiredFields) != 0 {
		t.Fatalf("metadata=%s", rec.Body)
	}
}

// adr:381
func TestFOCUSArtifactLimitProblem(t *testing.T) {
	problem := focusExportProblem(&focus.LimitError{Kind: "rows", Limit: api.MaxFOCUSExportRows, Observed: api.MaxFOCUSExportRows + 1})
	if problem.Status != http.StatusUnprocessableEntity || !strings.Contains(problem.Detail, "rows") {
		t.Fatalf("problem=%+v", problem)
	}
}
