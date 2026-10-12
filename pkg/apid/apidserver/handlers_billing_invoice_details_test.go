package apidserver_test

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
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/focus"
)

// adr:381
func TestFOCUSSignedInvoiceIngestion(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	var logs bytes.Buffer
	(*(*e.S()).Log()) = slog.New(slog.NewTextHandler(&logs, nil))
	(*(*e.S()).StripeWebhookSecret()) = apidserver.StripeWebhookSecretForTest
	(*e.S()).WithBillingProvider(&apidserver.ConsumeRefundProvider{})
	if err := (*e.Store()).UpdateAccountProviderCustomerID(context.Background(), (*e.Acct()).ID, "cus_test_123"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../../pkg/billing/testdata/invoices/stripe.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte("cus_invoice"), []byte("cus_test_123"))
	send := func(body []byte) {
		req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", bytes.NewReader(body))
		req.Header.Set("Stripe-Signature", stripe.SignForTest(body, apidserver.StripeWebhookSecretForTest, time.Now()))
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("webhook status=%d body=%s logs=%s", rec.Code, rec.Body, logs.String())
		}
	}
	send(raw)
	first, err := (*e.Store()).GetInvoiceByProviderID(context.Background(), (*e.Acct()).ID, "stripe", "in_invoice")
	if err != nil {
		t.Fatal(err)
	}
	send(raw) // the same signed event is replayed before export
	stored, err := (*e.Store()).GetInvoiceByID(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Details.Lines.Items[0] != first.Details.Lines.Items[0] {
		t.Fatal("delivery replay mutated line history")
	}
	for id, history := range first.Lifecycle.Records {
		if stored.Lifecycle.Records[id] != history {
			t.Fatal("delivery replay mutated export record history")
		}
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := (*e.Store()).CreateAPIKey(context.Background(), (*e.Acct()).ID, hash, "focus-export", api.ScopesUsageReadSurface); err != nil {
		t.Fatal(err)
	}
	(*e.Key()) = key
	rec := e.Do(t, http.MethodGet, "/v1/billing/focus?month=2026-09&format=csv", nil, nil)
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
	for _, row := range rows[1:] {
		history, ok := stored.Lifecycle.Records[row[9]]
		if !ok || row[6] != history.CreatedAt || row[10] != history.UpdatedAt {
			t.Fatal("download did not use persisted record timestamps")
		}
	}
	rec = e.Do(t, http.MethodGet, "/v1/billing/focus?month=2026-09&format=metadata", nil, nil)
	var m focus.Metadata
	if json.Unmarshal(rec.Body.Bytes(), &m) != nil || m.Projection.SourceCoverage.DetailedInvoices != 1 || len(m.Projection.MissingRequiredFields) != 0 {
		t.Fatalf("metadata=%s", rec.Body)
	}
	if m.Projection.SourceCoverage.UntrackedLifecycleRecords != 0 || m.Projection.SourceCoverage.LegacyLifecycleRecords != 0 {
		t.Fatal("new signed invoice has a false lifecycle coverage gap")
	}
}

// adr:381
func TestFOCUSArtifactLimitProblem(t *testing.T) {
	problem := apidserver.FocusExportProblem(&focus.LimitError{Kind: "rows", Limit: api.MaxFOCUSExportRows, Observed: api.MaxFOCUSExportRows + 1})
	if problem.Status != http.StatusUnprocessableEntity || !strings.Contains(problem.Detail, "rows") {
		t.Fatalf("problem=%+v", problem)
	}
}
