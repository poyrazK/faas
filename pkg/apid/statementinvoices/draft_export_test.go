package statementinvoices

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

type draftExportHost struct {
	issueHost
	consumer state.APIConsumer
}

func (h draftExportHost) ConsumerStatements(http.ResponseWriter, *http.Request, state.Account) (state.App, state.APIConsumer, state.APIConsumerUsageStatementStore, bool) {
	return state.App{ID: h.consumer.AppID}, h.consumer, h.store, true
}

func TestIndividualStripeDraftCustomer(t *testing.T) {
	const customer = "cus_Verified"
	const invoiceID = "in_draft_fixture"
	for _, tc := range []struct {
		name          string
		link          bool
		pinned        bool
		switchMode    bool
		switchAccount bool
		wantStatus    int
		wantCreates   int32
	}{
		{name: "uses the verified customer link", link: true, wantStatus: http.StatusOK, wantCreates: 1},
		{name: "resumes with the pinned customer", pinned: true, wantStatus: http.StatusOK},
		{name: "ignores links from the other mode", link: true, switchMode: true, wantStatus: http.StatusUnprocessableEntity},
		{name: "ignores links from another account", link: true, switchAccount: true, wantStatus: http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			account := state.Account{ID: uuid.NewString()}
			store := state.NewMemStore()
			consumer, err := store.CreateAPIConsumer(ctx, account.ID, uuid.NewString(), "crm-1004", "Customer")
			if err != nil {
				t.Fatal(err)
			}
			period := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
			statement, _, err := store.CreateAPIConsumerUsageStatement(ctx, state.APIConsumerUsageStatementInput{
				AccountID: account.ID, AppID: consumer.AppID, ConsumerID: consumer.ID, Revision: 1,
				PeriodStart: period, PeriodEnd: period.AddDate(0, 1, 0), AsOf: period.AddDate(0, 1, 1),
				Currency: "USD", BillableUnits: 1, AmountMillicents: 3_000, Priced: true,
				Buckets: []state.APIConsumerUsageStatementBucket{{
					WindowStart: period, BillableUnits: 1, RateCardID: uuid.NewString(), Currency: "USD",
					PriceMillicentsPerUnit: 3_000, AmountMillicents: 3_000,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.FinalizeAPIConsumerUsageStatement(ctx, account.ID, consumer.AppID, consumer.ID, statement.ID); err != nil {
				t.Fatal(err)
			}
			conn, err := store.PutBillingConnection(ctx, state.BillingConnection{
				AccountID: account.ID, Provider: state.BillingProviderStripe, StripeAccountID: "acct_drafts",
				KeyLast4: "test", SealedSecret: []byte("rk_test_fixture"), Collection: state.CollectionNone,
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.link {
				if _, err := store.LinkStripeConsumerCustomer(ctx, state.StripeConsumerLink{
					AccountID: account.ID, ConsumerID: consumer.ID, StripeAccountID: conn.StripeAccountID, StripeCustomerID: customer,
				}, conn.UpdatedAt); err != nil {
					t.Fatal(err)
				}
			}
			body := `{"livemode":false}`
			if tc.switchMode {
				conn.Livemode = true
				conn.SealedSecret = []byte("rk_live_fixture")
				body = `{"livemode":true}`
			}
			if tc.switchAccount {
				conn.StripeAccountID = "acct_other"
			}
			if tc.switchMode || tc.switchAccount {
				if _, err := store.PutBillingConnection(ctx, conn); err != nil {
					t.Fatal(err)
				}
			}
			if tc.pinned {
				export, _, err := store.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{
					AccountID: account.ID, SourceKind: state.StatementSourceAppConsumer, StatementID: statement.ID,
					Provider: state.BillingProviderStripe, Customer: customer, Currency: "USD", AmountMinor: 3,
					Collection: state.CollectionNone,
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.SetStatementInvoiceExportInvoice(ctx, account.ID, export.ID, invoiceID); err != nil {
					t.Fatal(err)
				}
			}
			var creates, items atomic.Int32
			stripe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "POST /v1/invoices":
					creates.Add(1)
					if r.FormValue("customer") != customer {
						t.Errorf("invoice customer = %q, want %s", r.FormValue("customer"), customer)
					}
					_, _ = w.Write([]byte(`{"id":"` + invoiceID + `"}`))
				case "GET /v1/invoiceitems":
					_, _ = w.Write([]byte(`{"data":[]}`))
				case "POST /v1/invoiceitems":
					items.Add(1)
					if r.FormValue("customer") != customer || r.FormValue("invoice") != invoiceID {
						t.Errorf("invoice item customer = %q, invoice = %q", r.FormValue("customer"), r.FormValue("invoice"))
					}
				default:
					t.Errorf("unexpected Stripe request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(stripe.Close)
			service := Service{
				Host:   draftExportHost{issueHost: issueHost{store: store, accountID: account.ID}, consumer: consumer},
				Stripe: &stripeinvoice.Client{BaseURL: stripe.URL, HTTP: stripe.Client()},
			}
			send := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, "/invoice/draft", strings.NewReader(body))
				r.SetPathValue("statement_id", statement.ID)
				w := httptest.NewRecorder()
				service.ExportAppConsumerStatementDraft(w, r, account)
				return w
			}
			w := send()
			if w.Code != tc.wantStatus {
				t.Fatalf("draft status = %d, want %d: %s", w.Code, tc.wantStatus, w.Body)
			}
			wantItems := int32(0)
			if tc.wantStatus == http.StatusOK {
				wantItems = 1
				verify := func(w *httptest.ResponseRecorder) {
					t.Helper()
					var export api.StatementInvoiceExportResponse
					if err := json.Unmarshal(w.Body.Bytes(), &export); err != nil || w.Code != http.StatusOK ||
						export.Customer != customer || export.InvoiceID != invoiceID || export.Collection != state.CollectionNone {
						t.Fatalf("draft response = %d %s", w.Code, w.Body)
					}
				}
				verify(w)
				verify(send())
			}
			if creates.Load() != tc.wantCreates || items.Load() != wantItems {
				t.Fatalf("Stripe received %d invoice creations and %d items, want %d and %d", creates.Load(), items.Load(), tc.wantCreates, wantItems)
			}
		})
	}
}
