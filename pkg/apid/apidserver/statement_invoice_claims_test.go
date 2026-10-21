package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261019180000 — an export the provider refused before creating an
// invoice can be retried with the right customer as a new attempt, and an
// export another attempt holds answers 409 in progress instead of creating
// a second invoice.
func TestStatementInvoiceExportRetargetAndClaim(t *testing.T) {
	f := newBillingRunFixture(t)
	apidserver.MustSeedApp(t, f.e, "retarget")
	if res := f.e.Do(t, http.MethodPost, "/v1/apps/retarget/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "EUR", PriceMillicentsPerUnit: 1250, EffectiveFrom: &f.month}, nil); res.Code != http.StatusCreated {
		t.Fatalf("rate card: %d %s", res.Code, res.Body)
	}
	wrong := f.consumer(t, "retarget", "cus_Wrong", 300, "")
	path := finalizedMonthStatement(t, f, "retarget", wrong.ID)

	f.stripe.rejectCustomer = "cus_Wrong"
	if res := f.e.Do(t, http.MethodPost, path, api.CreateStatementInvoiceRequest{}, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("export to a missing customer: %d %s, want 422", res.Code, res.Body)
	}
	export := exportStatement(t, f, path, api.CreateStatementInvoiceRequest{Customer: "cus_Right"}, http.StatusOK)
	if export.Customer != "cus_Right" || export.InvoiceID != "in_1" || f.stripe.invoices != 1 || len(f.stripe.invoiceKeys) != 2 ||
		!strings.HasSuffix(f.stripe.invoiceKeys[1], "-a1-invoice") || f.stripe.invoiceKeys[0] == f.stripe.invoiceKeys[1] {
		t.Fatalf("retargeted export = %+v, invoice keys = %v", export, f.stripe.invoiceKeys)
	}
	if again := exportStatement(t, f, path, api.CreateStatementInvoiceRequest{Customer: "cus_Other"}, http.StatusOK); again.Customer != "cus_Right" {
		t.Fatalf("completed export changed customer: %+v", again)
	}

	// Another attempt holds the next export: no second invoice.
	held := f.consumer(t, "retarget", "cus_Held", 200, "")
	heldPath := finalizedMonthStatement(t, f, "retarget", held.ID)
	statementID := strings.TrimSuffix(strings.TrimPrefix(heldPath[strings.Index(heldPath, "/usage-statements/"):], "/usage-statements/"), "/invoice")
	store := *f.e.Store()
	ctx := context.Background()
	begun, _, err := store.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{AccountID: (*f.e.Acct()).ID,
		SourceKind: state.StatementSourceAppConsumer, StatementID: statementID, Provider: state.BillingProviderStripe,
		Customer: "cus_Held", Currency: "EUR", AmountMinor: 250})
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	if _, err := store.ClaimStatementInvoiceExport(ctx, (*f.e.Acct()).ID, begun.ID, token, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	res := f.e.Do(t, http.MethodPost, heldPath, api.CreateStatementInvoiceRequest{}, nil)
	var problem api.Problem
	if res.Code != http.StatusConflict || json.Unmarshal(res.Body.Bytes(), &problem) != nil || problem.Code != api.CodeStatementInvoiceExportInProgress ||
		f.stripe.invoices != 1 {
		t.Fatalf("export while held: %d %s (invoices %d), want 409 in progress", res.Code, res.Body, f.stripe.invoices)
	}
	if err := store.ReleaseStatementInvoiceExport(ctx, (*f.e.Acct()).ID, begun.ID, token); err != nil {
		t.Fatal(err)
	}
	if done := exportStatement(t, f, heldPath, api.CreateStatementInvoiceRequest{}, http.StatusOK); done.InvoiceID != "in_2" {
		t.Fatalf("export after release = %+v", done)
	}
}
