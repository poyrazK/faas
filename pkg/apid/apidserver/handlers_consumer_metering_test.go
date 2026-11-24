package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

type meteringRequest func(*testing.T, string, string, any, map[string]string) *httptest.ResponseRecorder

func TestAPIConsumerMeteredGenerateReconciles(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		env := apidserver.Setup(t, api.PlanHobby)
		checkMeteredGenerate(t, *env.Store(), env.Acct().ID, env.Do)
	})
	t.Run("postgres", func(t *testing.T) {
		env := apidserver.SetupPGHandler(t, api.PlanHobby)
		checkMeteredGenerate(t, *env.Store(), env.Acct().ID, env.Do)
	})
}

func checkMeteredGenerate(t *testing.T, store state.Store, accountID string, do meteringRequest) {
	t.Helper()
	ctx := context.Background()
	app, err := store.CreateApp(ctx, state.App{AccountID: accountID, Slug: "metered-api", Type: state.AppTypeApp,
		Status: state.AppActive, MaxConcurrency: 1, PublicAuthMode: api.PlanHobby.PublicAuthModeDefault()})
	if err != nil {
		t.Fatal(err)
	}
	appPath := "/v1/apps/" + app.Slug
	var consumer api.APIConsumerResponse
	meteringResponse(t, do, http.MethodPost, appPath+"/consumers", api.CreateAPIConsumerRequest{
		ExternalRef: "test-consumer", Name: "Test consumer",
	}, http.StatusCreated, &consumer)

	// All three requests are observed in the same minute. One weighted
	// request unit is included; the two token rates share 100 free tokens.
	start := time.Now().UTC().Add(time.Minute).Truncate(time.Minute)
	end := start.Add(time.Minute)
	var modelCard, requestCard api.APIConsumerRateCardResponse
	meteringResponse(t, do, http.MethodPost, appPath+"/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "USD", PriceMillicentsPerUnit: 3, Unit: "app:output_tokens", EffectiveFrom: &start,
		IncludedUnitsPerMonth: 100, ModelPrices: map[string]int64{"standard": 2, "premium": 5},
	}, http.StatusCreated, &modelCard)
	meteringResponse(t, do, http.MethodPost, appPath+"/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "USD", PriceMillicentsPerUnit: 3, EffectiveFrom: &start,
		IncludedUnitsPerMonth: 1, RouteWeights: map[string]int64{"POST /generate": 2},
	}, http.StatusCreated, &requestCard)
	if modelCard.ModelPrices["premium"] != 5 {
		t.Fatalf("model prices = %+v", modelCard.ModelPrices)
	}
	// A model alias must have exactly one rate stream.
	meteringResponse(t, do, http.MethodPost, appPath+"/rate-cards", api.CreateAPIConsumerRateCardRequest{
		Currency: "USD", PriceMillicentsPerUnit: 9, Unit: "app:output_tokens__premium", EffectiveFrom: &start,
	}, http.StatusUnprocessableEntity, nil)

	ledger := store.(interface {
		RecordAPIConsumerUsage(context.Context, state.APIConsumerUsageEvent) (bool, error)
	})
	record := func(model string, units int64) {
		t.Helper()
		event := state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: accountID, AppID: app.ID, ConsumerKey: consumer.ID,
			WindowStart: start, RequestCount: 1, BillableUnits: 1, BillingRoute: "POST /generate",
			MeterUnits: map[string]int64{state.ModelMeterName("output_tokens", model): units}}
		if applied, err := ledger.RecordAPIConsumerUsage(ctx, event); !applied || err != nil {
			t.Fatalf("record usage: applied=%v err=%v", applied, err)
		}
		if applied, err := ledger.RecordAPIConsumerUsage(ctx, event); applied || err != nil {
			t.Fatalf("replay usage: applied=%v err=%v", applied, err)
		}
	}
	record("standard", 40)
	record("premium", 120)
	record("standard", 60)

	path := appPath + "/consumers/" + consumer.ID
	usageUntil := time.Date(start.Year(), start.Month(), start.Day()+1, 0, 0, 0, 0, time.UTC)
	query := "?breakdown=true&until=" + url.QueryEscape(usageUntil.Format(time.RFC3339))
	var usage api.APIConsumerUsageResponse
	meteringResponse(t, do, http.MethodGet, path+"/usage"+query, nil, http.StatusOK, &usage)
	if usage.RequestCount != 3 || len(usage.RouteBreakdown) != 1 || usage.RouteBreakdown[0] != (api.APIConsumerRouteUsage{Route: "POST /generate", BillableRequests: 3}) ||
		len(usage.MeterBreakdown) != 2 || usage.MeterBreakdown[0] != (api.APIConsumerMeterUsage{Meter: "output_tokens__premium", Units: 120}) ||
		usage.MeterBreakdown[1] != (api.APIConsumerMeterUsage{Meter: "output_tokens__standard", Units: 100}) {
		t.Fatalf("usage did not reconcile: %+v", usage)
	}

	var quote api.APIConsumerUsageQuoteResponse
	meteringResponse(t, do, http.MethodGet, path+"/usage/quote"+query, nil, http.StatusOK, &quote)
	if len(quote.RouteBreakdown) != 1 || len(quote.MeterBreakdown) != 2 || !quote.Priced || quote.AmountMillicents != 315 {
		t.Fatalf("quote did not reconcile: %+v", quote)
	}
	if got := quote.RouteBreakdown[0]; got.Route != "POST /generate" || got.RateCardID != requestCard.ID ||
		got.BillableUnits != 6 || got.IncludedUnits != 1 || got.ChargedUnits != 5 || got.AmountMillicents != 15 {
		t.Fatalf("weighted request price: %+v", got)
	}
	for _, want := range []api.APIConsumerQuoteBreakdown{
		{Unit: "app:output_tokens", Model: "premium", RateCardID: modelCard.ID, BillableUnits: 120,
			IncludedUnits: 100, ChargedUnits: 20, PriceMillicentsPerUnit: 5, AmountMillicents: 100},
		{Unit: "app:output_tokens", Model: "standard", RateCardID: modelCard.ID, BillableUnits: 100,
			ChargedUnits: 100, PriceMillicentsPerUnit: 2, AmountMillicents: 200},
	} {
		at := 0
		if want.Model == "standard" {
			at = 1
		}
		if got := quote.MeterBreakdown[at]; got != want {
			t.Fatalf("model price = %+v, want %+v", got, want)
		}
	}

	var draft, finalized api.APIConsumerUsageStatementResponse
	period := api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &start, PeriodEnd: &end}
	meteringResponse(t, do, http.MethodPost, path+"/usage-statements", period, http.StatusCreated, &draft)
	meteringResponse(t, do, http.MethodPost, path+"/usage-statements/"+draft.ID+"/finalize", nil, http.StatusOK, &finalized)
	if finalized.Status != "finalized" || finalized.AmountMillicents != quote.AmountMillicents || finalized.BillableUnits != quote.BillableUnits ||
		len(finalized.Buckets) != 3 || finalized.Buckets[1].Model != "premium" || finalized.Buckets[2].Model != "standard" {
		t.Fatalf("statement did not reconcile: %+v", finalized)
	}
	record("standard", 30)
	var adjustment api.APIConsumerUsageStatementResponse
	meteringResponse(t, do, http.MethodPost, path+"/usage-statements", period, http.StatusCreated, &adjustment)
	if adjustment.Revision != 2 || adjustment.AmountMillicents != 66 || len(adjustment.Buckets) != 2 || adjustment.Buckets[1].Model != "standard" {
		t.Fatalf("late usage did not stay in its model: %+v", adjustment)
	}

	// A new alias recorded after the priced draft must hold that draft too.
	record("new_model", 17)
	var problem api.Problem
	meteringResponse(t, do, http.MethodPost, path+"/usage-statements/"+adjustment.ID+"/finalize", nil, http.StatusConflict, &problem)
	if !strings.Contains(problem.Detail, "new_model") {
		t.Fatalf("finalization should explain the pricing gap: %+v", problem)
	}

	var heldQuote api.APIConsumerUsageQuoteResponse
	meteringResponse(t, do, http.MethodGet, path+"/usage/quote"+query, nil, http.StatusOK, &heldQuote)
	if heldQuote.Priced || heldQuote.UnpricedUnits != 17 || heldQuote.AmountMillicents != 387 ||
		len(heldQuote.MeterBreakdown) != 3 {
		t.Fatalf("missing model must be visible without changing known rates: %+v", heldQuote)
	}
	missing := heldQuote.MeterBreakdown[0]
	if missing.Model != "new_model" || missing.RateCardID != "" || missing.BillableUnits != 17 ||
		missing.IncludedUnits != 0 || missing.ChargedUnits != 0 {
		t.Fatalf("unpriced model breakdown = %+v", missing)
	}

	var held api.APIConsumerUsageStatementResponse
	meteringResponse(t, do, http.MethodPost, path+"/usage-statements", period, http.StatusCreated, &held)
	if held.Revision != 3 || held.Priced || held.UnpricedUnits != 17 || held.AmountMillicents != 72 ||
		len(held.Buckets) != 3 || held.Buckets[1].Model != "new_model" || held.Buckets[1].RateCardID != "" {
		t.Fatalf("missing model must be in the revised draft: %+v", held)
	}
	meteringResponse(t, do, http.MethodPost, path+"/usage-statements/"+held.ID+"/finalize", nil, http.StatusConflict, nil)
	meteringResponse(t, do, http.MethodPost, path+"/usage-statements/"+finalized.ID+"/finalize", nil, http.StatusOK, nil)
}

func TestAPIConsumerUsageWaiver(t *testing.T) {
	t.Run("memory and Stripe test double", func(t *testing.T) {
		withBillingKeys(t)
		env := apidserver.Setup(t, api.PlanHobby)
		stripe := &fakeStripe{}
		server := httptest.NewServer(stripe)
		t.Cleanup(server.Close)
		(*env.S()).StatementInvoices().Stripe = &stripeinvoice.Client{BaseURL: server.URL, HTTP: server.Client()}
		meteringResponse(t, env.Do, http.MethodPut, "/v1/account/billing-connections/stripe",
			api.PutBillingConnectionRequest{SecretKey: "rk_test_51abcdWXYZ"}, http.StatusOK, nil)
		checkUsageWaiver(t, *env.Store(), env.Acct().ID, env.Do, "cus_ABC123", func(path string, statement api.APIConsumerUsageStatementResponse) {
			stripe.failNextAdd = true
			meteringResponse(t, env.Do, http.MethodPost, path, struct{}{}, http.StatusBadGateway, nil)
			var invoice api.StatementInvoiceExportResponse
			meteringResponse(t, env.Do, http.MethodPost, path, struct{}{}, http.StatusOK, &invoice)
			checkMeteringInvoiceReplay(t, env.Do, path, statement, invoice)
			if stripe.invoices != 1 || len(stripe.items[invoice.InvoiceID]) != len(meteringInvoiceLines) {
				t.Fatalf("invoices = %d, items = %v", stripe.invoices, stripe.items)
			}
			checkMeteringInvoiceLines(t, stripe.amounts)
		})
	})
	t.Run("postgres", func(t *testing.T) {
		env := apidserver.SetupPGHandler(t, api.PlanHobby)
		checkUsageWaiver(t, *env.Store(), env.Acct().ID, env.Do, "cus_ABC123", nil)
	})
}

var meteringInvoiceLines = []string{
	"0:100 output tokens included at no charge",
	"100:20 output tokens at 0.05 USD each",
	"200:100 output tokens at 0.02 USD each",
}

func checkMeteringInvoiceLines(t *testing.T, got []string) {
	t.Helper()
	lines := slices.Clone(got)
	slices.Sort(lines)
	if !slices.Equal(lines, meteringInvoiceLines) {
		t.Fatalf("invoice lines = %v, want %v", lines, meteringInvoiceLines)
	}
}

func checkMeteringInvoiceReplay(t *testing.T, do meteringRequest, path string, statement api.APIConsumerUsageStatementResponse, invoice api.StatementInvoiceExportResponse) {
	t.Helper()
	// The fixture's USD prices yield exact cents, with 1,000 millicents per cent.
	if invoice.InvoiceID == "" || invoice.Status != state.StatementInvoiceExportCompleted || invoice.StatementID != statement.ID ||
		invoice.Currency != statement.Currency || invoice.AmountMinor*1000 != statement.AmountDueMillicents ||
		invoice.Livemode || invoice.Collection != state.CollectionNone || !strings.Contains(invoice.DashboardURL, "/test/invoices/") {
		t.Fatalf("invoice = %+v, want a sandbox draft for %s with %d millicents due", invoice, statement.ID, statement.AmountDueMillicents)
	}
	var replay api.StatementInvoiceExportResponse
	meteringResponse(t, do, http.MethodPost, path, struct{}{}, http.StatusOK, &replay)
	if replay.InvoiceID != invoice.InvoiceID || replay.AmountMinor != invoice.AmountMinor {
		t.Fatalf("invoice export replay = %+v, want %s with %d cents due", replay, invoice.InvoiceID, invoice.AmountMinor)
	}
}

func checkUsageWaiver(t *testing.T, store state.Store, accountID string, do meteringRequest, customerID string, checkInvoice func(string, api.APIConsumerUsageStatementResponse)) {
	t.Helper()
	ctx := context.Background()
	app, err := store.CreateApp(ctx, state.App{AccountID: accountID, Slug: "waiver-test", Type: state.AppTypeApp,
		Status: state.AppActive, MaxConcurrency: 1, PublicAuthMode: api.PlanHobby.PublicAuthModeDefault()})
	if err != nil {
		t.Fatal(err)
	}
	appPath := "/v1/apps/" + app.Slug
	var consumer api.APIConsumerResponse
	meteringResponse(t, do, http.MethodPost, appPath+"/consumers", api.CreateAPIConsumerRequest{
		ExternalRef: customerID, Name: "Waiver test consumer",
	}, http.StatusCreated, &consumer)

	start := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	end := start.Add(time.Minute)
	if _, err := store.(state.APIConsumerRateCardStore).CreateAPIConsumerRateCardVersion(ctx, state.APIConsumerRateCardInput{
		AccountID: accountID, AppID: app.ID, Unit: "app:output_tokens", Currency: "USD", EffectiveFrom: start,
		PriceMillicentsPerUnit: 1000, IncludedUnitsPerMonth: 100, ModelPrices: map[string]int64{"premium": 5000, "standard": 2000},
	}); err != nil {
		t.Fatal(err)
	}
	record := func(model string, units int64) {
		t.Helper()
		applied, err := store.(state.ConsumerUsageStore).RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(), AccountID: accountID,
			AppID: app.ID, ConsumerKey: consumer.ID, WindowStart: start, RequestCount: 1, BillableUnits: 1,
			MeterUnits: map[string]int64{state.ModelMeterName("output_tokens", model): units}})
		if err != nil || !applied {
			t.Fatalf("record %s: applied=%v err=%v", model, applied, err)
		}
	}
	record("premium", 120)
	record("standard", 100)
	record("unlisted", 17)
	path := appPath + "/consumers/" + consumer.ID
	statements := path + "/usage-statements"
	period := api.CreateAPIConsumerUsageStatementRequest{PeriodStart: &start, PeriodEnd: &end}
	var held api.APIConsumerUsageStatementResponse
	meteringResponse(t, do, http.MethodPost, statements, period, http.StatusCreated, &held)
	if held.Priced || held.UnpricedUnits != 17 || held.AmountMillicents != 300000 {
		t.Fatalf("held draft = %+v", held)
	}
	meteringResponse(t, do, http.MethodPost, statements+"/"+held.ID+"/finalize", nil, http.StatusConflict, nil)
	waive := period
	selection := api.APIConsumerUsageWaiverRequest{
		SourceStatementID: held.ID, Unit: "app:output_tokens", Model: "unlisted", Since: start, Until: end,
		Units: 16, Reason: "Sandbox rollout approval",
	}
	waive.Waiver = &selection
	meteringResponse(t, do, http.MethodPost, statements, waive, http.StatusConflict, nil)
	selection.Units = 17
	var approved, replay api.APIConsumerUsageStatementResponse
	receipt := map[string]string{"Idempotency-Key": uuid.NewString()}
	response := do(t, http.MethodPost, statements, waive, receipt)
	if err := json.Unmarshal(response.Body.Bytes(), &approved); err != nil || response.Code != http.StatusCreated {
		t.Fatalf("approve waiver: %d %s", response.Code, response.Body)
	}
	deployKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, accountID, hash, "deploy-only", []string{api.ScopeDeployWrite}); err != nil {
		t.Fatal(err)
	}
	receipt["Authorization"] = "Bearer " + deployKey
	if res := do(t, http.MethodPost, statements, waive, receipt); res.Code != http.StatusForbidden {
		t.Fatalf("deploy-only key reused the approval receipt: %d %s", res.Code, res.Body)
	}
	if !approved.Priced || approved.UnpricedUnits != 0 || approved.AmountMillicents != held.AmountMillicents || approved.Revision != 2 ||
		approved.Waiver == nil || approved.Waiver.SourceStatementID != held.ID || approved.Waiver.ApprovedBy != accountID ||
		approved.Waiver.ApprovedAt.IsZero() || approved.Waiver.Units != 17 || approved.Waiver.Reason != selection.Reason {
		t.Fatalf("approval = %+v", approved)
	}
	meteringResponse(t, do, http.MethodPost, statements, waive, http.StatusOK, &replay)
	if replay.ID != approved.ID {
		t.Fatalf("replay returned %s, want %s", replay.ID, approved.ID)
	}
	selection.Reason = "Different approval"
	meteringResponse(t, do, http.MethodPost, statements, waive, http.StatusConflict, nil)
	selection.Reason = "Sandbox rollout approval"
	quoteUntil := time.Date(start.Year(), start.Month(), start.Day()+1, 0, 0, 0, 0, time.UTC)
	quotePath := path + "/usage/quote?breakdown=true&until=" + url.QueryEscape(quoteUntil.Format(time.RFC3339))
	var quote api.APIConsumerUsageQuoteResponse
	meteringResponse(t, do, http.MethodGet, quotePath, nil, http.StatusOK, &quote)
	if !quote.Priced || quote.UnpricedUnits != 0 || quote.AmountMillicents != held.AmountMillicents || len(quote.MeterBreakdown) != 3 ||
		quote.MeterBreakdown[2].Model != "unlisted" || quote.MeterBreakdown[2].WaivedUnits != 17 {
		t.Fatalf("quote = %+v", quote)
	}
	var finalized api.APIConsumerUsageStatementResponse
	meteringResponse(t, do, http.MethodPost, statements+"/"+approved.ID+"/finalize", nil, http.StatusOK, &finalized)
	if finalized.ID != approved.ID || finalized.Status != string(state.APIConsumerUsageStatementFinalized) || finalized.AmountDueMillicents != held.AmountMillicents {
		t.Fatalf("finalized metering statement = %+v, want %s with %d millicents due", finalized, approved.ID, held.AmountMillicents)
	}
	if checkInvoice != nil {
		checkInvoice(statements+"/"+finalized.ID+"/invoice", finalized)
	}

	record("standard", 5)
	var adjustment api.APIConsumerUsageStatementResponse
	meteringResponse(t, do, http.MethodPost, statements, period, http.StatusCreated, &adjustment)
	if !adjustment.Priced || adjustment.Revision != 3 || adjustment.BillableUnits != 5 || adjustment.AmountMillicents != 10000 ||
		len(adjustment.Buckets) != 1 || adjustment.Buckets[0].Model != "standard" {
		t.Fatalf("priced late adjustment = %+v", adjustment)
	}
	meteringResponse(t, do, http.MethodPost, statements+"/"+adjustment.ID+"/finalize", nil, http.StatusOK, nil)
	var original api.APIConsumerUsageStatementResponse
	meteringResponse(t, do, http.MethodGet, statements+"/"+finalized.ID, nil, http.StatusOK, &original)
	if original.ID != finalized.ID || original.Status != finalized.Status || original.AmountDueMillicents != finalized.AmountDueMillicents {
		t.Fatalf("original statement after adjustment = %+v, want %+v", original, finalized)
	}

	record("unlisted", 3)
	meteringResponse(t, do, http.MethodGet, quotePath, nil, http.StatusOK, &quote)
	if quote.Priced || quote.UnpricedUnits != 3 || quote.AmountMillicents != held.AmountMillicents+adjustment.AmountMillicents {
		t.Fatalf("late quote = %+v", quote)
	}
	var late api.APIConsumerUsageStatementResponse
	meteringResponse(t, do, http.MethodPost, statements, period, http.StatusCreated, &late)
	if late.Priced || late.Revision != 4 || late.UnpricedUnits != 3 || late.AmountMillicents != 0 {
		t.Fatalf("late adjustment = %+v", late)
	}
	meteringResponse(t, do, http.MethodPost, statements+"/"+late.ID+"/finalize", nil, http.StatusConflict, nil)
	meteringResponse(t, do, http.MethodPost, statements, waive, http.StatusOK, &replay)
	if replay.ID != approved.ID {
		t.Fatalf("late replay returned %s, want %s", replay.ID, approved.ID)
	}
	selection.SourceStatementID, selection.Units = late.ID, 3
	meteringResponse(t, do, http.MethodPost, statements, waive, http.StatusCreated, &late)
	// The ledger guard also catches late usage written after this approval
	// and before its draft can be finalized.
	record("unlisted", 2)
	meteringResponse(t, do, http.MethodPost, statements+"/"+late.ID+"/finalize", nil, http.StatusConflict, nil)
	meteringResponse(t, do, http.MethodPost, statements, period, http.StatusCreated, &late)
	if late.UnpricedUnits != 2 || late.AmountMillicents != 0 {
		t.Fatalf("additional late usage = %+v", late)
	}
	selection.SourceStatementID, selection.Units = late.ID, 2
	meteringResponse(t, do, http.MethodPost, statements, waive, http.StatusCreated, &late)
	meteringResponse(t, do, http.MethodPost, statements+"/"+late.ID+"/finalize", nil, http.StatusOK, nil)
}

func meteringResponse(t *testing.T, do meteringRequest, method, path string, body any, status int, into any) {
	t.Helper()
	response := do(t, method, path, body, nil)
	if response.Code != status {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, response.Code, status, response.Body)
	}
	if into != nil {
		if err := json.Unmarshal(response.Body.Bytes(), into); err != nil {
			t.Fatal(err)
		}
	}
}
