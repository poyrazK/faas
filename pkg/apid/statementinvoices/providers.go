package statementinvoices

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/lagoinvoice"
	"github.com/onebox-faas/faas/pkg/billing/paddleinvoice"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// errKeyRejected wraps a provider's own key rejection, so the payment sync
// can stop using a key without knowing the provider.
var errKeyRejected = errors.New("billing provider rejected the key")

// providerMaxItems bounds the items Paddle and Lago receive; a statement
// with more lines exports as one summary item.
const providerMaxItems = 100

// connection is an opened billing connection.
type connection struct {
	key      string
	apiURL   string
	livemode bool
	// collection and daysUntilDue are the connection's collection mode
	// (ADR-1311).
	collection   string
	daysUntilDue int
	automaticTax bool
}

// provider is one billing system statements export to (ADR-1507).
type provider interface {
	title() string
	// parseKey checks a key's format and returns its mode.
	parseKey(key string) (livemode bool, err error)
	checkKey(ctx context.Context, conn connection) error
	// customer resolves the request's customer, or externalRef when it is
	// one of this provider's customer IDs.
	customer(requested, externalRef string) (string, *api.Problem)
	currencyProblem(currency string) *api.Problem
	// push creates the invoice once, or adopts the one an earlier attempt
	// created; record stores its ID before anything else is sent. push calls
	// reject when the provider refused to create the invoice, so nothing
	// exists there (ADR-20261019180000).
	push(ctx context.Context, conn connection, export *state.StatementInvoiceExport, src statementInvoiceSource, resumed bool,
		record func(invoiceID string) error, reject func()) *api.Problem
	payment(ctx context.Context, conn connection, invoiceID string) (state.StatementInvoicePayment, bool, error)
	dashboardURL(livemode bool, invoiceID string) string
}

func (s *Service) provider(name string) provider {
	switch name {
	case state.BillingProviderStripe:
		return stripeProvider{client: s.stripeInvoiceClient()}
	case state.BillingProviderPaddle:
		return paddleProvider{client: s.paddleClient()}
	case state.BillingProviderLago:
		return lagoProvider{client: s.lagoClient()}
	}
	return nil
}

func dashboardURL(provider string, livemode bool, invoiceID string) string {
	switch provider {
	case state.BillingProviderStripe:
		return stripeinvoice.DashboardURL(livemode, invoiceID)
	case state.BillingProviderPaddle:
		return paddleinvoice.DashboardURL(livemode, invoiceID)
	}
	return ""
}

func providerProblem(status int, title, detail string) *api.Problem {
	code := api.CodeValidation
	if status == http.StatusBadGateway {
		code = api.CodeBillingProviderUnavailable
	}
	return api.NewProblem(status, code, title, detail)
}

var customerTextRE = regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,255}$`)

func resolveCustomer(requested, externalRef string, valid *regexp.Regexp, title, detail string) (string, *api.Problem) {
	customer := strings.TrimSpace(requested)
	if customer == "" && valid.MatchString(externalRef) {
		customer = externalRef
	}
	if !valid.MatchString(customer) {
		return "", api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, title+" customer required", detail)
	}
	return customer, nil
}

// refused reports a provider's definitive refusal of a request: a rejected
// key or a 4xx other than a timeout, a conflict or a rate limit, which the
// next attempt may get past.
func refused(err error, keyRejected error, status int) bool {
	if errors.Is(err, keyRejected) {
		return true
	}
	return status >= 400 && status < 500 && status != http.StatusRequestTimeout && status != http.StatusConflict &&
		status != http.StatusTooManyRequests
}

func stripeRefused(err error) bool {
	var e *stripeinvoice.Error
	status := 0
	if errors.As(err, &e) {
		status = e.Status
	}
	return refused(err, stripeinvoice.ErrKeyRejected, status)
}

func paddleRefused(err error) bool {
	var e *paddleinvoice.Error
	status := 0
	if errors.As(err, &e) {
		status = e.Status
	}
	return refused(err, paddleinvoice.ErrKeyRejected, status)
}

func lagoRefused(err error) bool {
	var e *lagoinvoice.Error
	status := 0
	if errors.As(err, &e) {
		status = e.Status
	}
	return refused(err, lagoinvoice.ErrKeyRejected, status)
}

// stripeIdempotencyKey names one Stripe request of an export attempt; the
// first attempt keeps the original keys so in-flight exports resume.
func stripeIdempotencyKey(export *state.StatementInvoiceExport, suffix string) string {
	if export.Attempt == 0 {
		return "gregale-" + export.ID + "-" + suffix
	}
	return fmt.Sprintf("gregale-%s-a%d-%s", export.ID, export.Attempt, suffix)
}

func periodDescription(src statementInvoiceSource) string {
	description := fmt.Sprintf("Usage from %s to %s", src.start.UTC().Format(time.DateOnly), src.end.UTC().Format(time.DateOnly))
	if src.revision > 1 {
		description += fmt.Sprintf(" (adjustment %d)", src.revision)
	}
	return description
}

// nonNegativeItems are a statement's items for providers that reject
// negative amounts: its lines, or one summary item when a line or the
// rounding is negative or there are too many lines.
func nonNegativeItems(src statementInvoiceSource) ([]statementInvoiceItem, *api.Problem) {
	if src.invoice.TotalMinor < 0 {
		return nil, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Statement cannot be invoiced",
			"the statement total is negative; issue a credit note in the billing provider instead")
	}
	items := statementInvoiceItems(src.invoice)
	summarize := len(items) > providerMaxItems
	for _, item := range items {
		summarize = summarize || item.amount < 0
	}
	if summarize {
		return []statementInvoiceItem{{amount: src.invoice.TotalMinor, description: periodDescription(src)}}, nil
	}
	return items, nil
}

// --- Stripe (ADR-1502) ---

var stripeCustomerIDRE = regexp.MustCompile(`^cus_[A-Za-z0-9]{1,250}$`)

type stripeProvider struct{ client *stripeinvoice.Client }

func (stripeProvider) title() string                       { return "Stripe" }
func (stripeProvider) parseKey(key string) (bool, error)   { return stripeinvoice.KeyLivemode(key) }
func (stripeProvider) currencyProblem(string) *api.Problem { return nil }
func (stripeProvider) dashboardURL(live bool, id string) string {
	return stripeinvoice.DashboardURL(live, id)
}

func (p stripeProvider) checkKey(ctx context.Context, conn connection) error {
	return p.client.CheckKey(ctx, conn.key)
}

func (stripeProvider) customer(requested, externalRef string) (string, *api.Problem) {
	return resolveCustomer(requested, externalRef, stripeCustomerIDRE, "Stripe",
		"pass customer as a Stripe customer ID (cus_…), or set the external_ref to one")
}

// stripeCallProblem explains a failed Stripe call without echoing the key.
func stripeCallProblem(err error) *api.Problem {
	var stripeErr *stripeinvoice.Error
	switch {
	case errors.Is(err, stripeinvoice.ErrKeyRejected):
		return providerProblem(http.StatusUnprocessableEntity, "Stripe rejected the key",
			"use a secret or restricted key with read and write access to invoices and invoice items")
	case errors.As(err, &stripeErr) && stripeErr.Status >= 400 && stripeErr.Status < 500:
		return providerProblem(http.StatusUnprocessableEntity, "Stripe rejected the request", stripeErr.Message)
	default:
		return providerProblem(http.StatusBadGateway, "Could not reach Stripe", "retry; an export resumes the same draft invoice")
	}
}

// push creates the draft invoice once and adds each line Stripe does not
// already hold; idempotency keys derive from the export.
func (p stripeProvider) push(ctx context.Context, conn connection, export *state.StatementInvoiceExport, src statementInvoiceSource,
	_ bool, record func(string) error, reject func()) *api.Problem {
	metadata := map[string]string{"gregale_statement_id": src.statementID, "gregale_statement_revision": strconv.Itoa(src.revision), "gregale_source": src.kind}
	if export.ProviderInvoiceID == "" {
		invoice, err := p.client.CreateInvoice(ctx, conn.key, stripeIdempotencyKey(export, "invoice"), stripeinvoice.InvoiceParams{
			Customer: export.Customer, Currency: export.Currency, Description: periodDescription(src), Metadata: metadata,
			CollectionMethod: stripeCollectionMethod(export.Collection), DaysUntilDue: export.DaysUntilDue, AutomaticTax: export.AutomaticTax})
		if err != nil {
			if stripeRefused(err) {
				reject()
			}
			return stripeCallProblem(err)
		}
		if err := record(invoice.ID); err != nil {
			return api.ErrInternal("could not record the Stripe invoice")
		}
	}
	existing, err := p.client.ExportedLines(ctx, conn.key, export.ProviderInvoiceID)
	if err != nil {
		return stripeCallProblem(err)
	}
	unit, err := billing.StripeMinorUnit(export.Currency)
	if err != nil {
		return statementInvoiceProblem(err)
	}
	for i, item := range statementInvoiceItems(src.invoice.In(unit)) {
		line := strconv.Itoa(i)
		if existing[line] {
			continue
		}
		lineMetadata := map[string]string{stripeinvoice.LineMetadataKey: line, "gregale_statement_id": src.statementID}
		if err := p.client.CreateInvoiceItem(ctx, conn.key, stripeIdempotencyKey(export, "item-"+line), stripeinvoice.ItemParams{
			Customer: export.Customer, InvoiceID: export.ProviderInvoiceID, Currency: export.Currency, AmountMinor: item.amount,
			Description: item.description, PeriodStart: src.start, PeriodEnd: src.end, Metadata: lineMetadata}); err != nil {
			return stripeCallProblem(err)
		}
	}
	return p.collect(ctx, conn, export)
}

// stripeCollectionMethod is the Stripe collection method for an export's
// collection mode (ADR-1311); drafts keep Stripe's default.
func stripeCollectionMethod(mode string) string {
	switch mode {
	case state.CollectionSend:
		return "send_invoice"
	case state.CollectionCharge:
		return "charge_automatically"
	}
	return ""
}

// collect finalizes a complete invoice whose export asks Stripe to send or
// charge it (ADR-1311). A resumed export finalizes only a draft, so the
// step is safe to repeat.
func (p stripeProvider) collect(ctx context.Context, conn connection, export *state.StatementInvoiceExport) *api.Problem {
	if export.Collection != state.CollectionSend && export.Collection != state.CollectionCharge {
		return nil
	}
	invoice, err := p.client.GetInvoice(ctx, conn.key, export.ProviderInvoiceID)
	if err != nil {
		return stripeCallProblem(err)
	}
	if invoice.Deleted || invoice.Status != "draft" {
		return nil
	}
	if err := p.client.FinalizeInvoice(ctx, conn.key, stripeIdempotencyKey(export, "finalize"), export.ProviderInvoiceID); err != nil {
		return stripeCallProblem(err)
	}
	return nil
}

func (p stripeProvider) payment(ctx context.Context, conn connection, invoiceID string) (state.StatementInvoicePayment, bool, error) {
	invoice, err := p.client.GetInvoice(ctx, conn.key, invoiceID)
	if errors.Is(err, stripeinvoice.ErrKeyRejected) {
		return state.StatementInvoicePayment{}, false, fmt.Errorf("%w: %w", errKeyRejected, err)
	}
	if err != nil {
		return state.StatementInvoicePayment{}, false, err
	}
	if invoice.Deleted {
		return state.StatementInvoicePayment{Status: state.InvoicePaymentDeleted}, true, nil
	}
	// Stripe writes some currencies differently from ISO 4217; Gregale keeps
	// ISO minor units (ADR-20261019100330).
	currency := strings.ToUpper(invoice.Currency)
	unit, unitErr := billing.StripeMinorUnit(currency)
	iso, _ := api.CurrencyMinorExponent(currency)
	if unitErr != nil {
		unit = billing.MinorUnit{Exponent: iso, Step: 1}
	}
	return knownPayment(state.StatementInvoicePayment{Status: invoice.Status, AmountPaidMinor: unit.ToISOMinor(max(invoice.AmountPaid, 0), iso),
		AmountRemainingMinor: unit.ToISOMinor(max(invoice.AmountRemaining, 0), iso), DueAt: invoice.DueDate, PaidAt: invoice.PaidAt}, invoice.HostedURL)
}

// --- Paddle (ADR-1507) ---

var paddleCustomerIDRE = regexp.MustCompile(`^ctm_[a-z0-9]{1,64}$`)

type paddleProvider struct{ client *paddleinvoice.Client }

func (paddleProvider) title() string                     { return "Paddle" }
func (paddleProvider) parseKey(key string) (bool, error) { return paddleinvoice.KeyLivemode(key) }
func (paddleProvider) dashboardURL(live bool, id string) string {
	return paddleinvoice.DashboardURL(live, id)
}

func (p paddleProvider) checkKey(ctx context.Context, conn connection) error {
	return p.client.CheckKey(ctx, conn.key)
}

func (paddleProvider) customer(requested, externalRef string) (string, *api.Problem) {
	return resolveCustomer(requested, externalRef, paddleCustomerIDRE, "Paddle",
		"pass customer as a Paddle customer ID (ctm_…), or set the external_ref to one")
}

func (paddleProvider) currencyProblem(currency string) *api.Problem {
	switch currency {
	case "USD", "EUR", "GBP":
		return nil
	}
	return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Currency not supported",
		"Paddle invoices manually collected transactions in USD, EUR or GBP only")
}

func paddleCallProblem(err error) *api.Problem {
	var paddleErr *paddleinvoice.Error
	switch {
	case errors.Is(err, paddleinvoice.ErrKeyRejected):
		return providerProblem(http.StatusUnprocessableEntity, "Paddle rejected the key",
			"use an API key with read and write access to transactions and read access to customers")
	case errors.As(err, &paddleErr) && paddleErr.Status >= 400 && paddleErr.Status < 500:
		return providerProblem(http.StatusUnprocessableEntity, "Paddle rejected the request", paddleErr.Detail)
	default:
		return providerProblem(http.StatusBadGateway, "Could not reach Paddle", "retry; an export resumes the same transaction")
	}
}

// push creates a draft, manually collected transaction once, then bills it
// when the export sends (ADR-20261019100333). Paddle has no idempotency
// keys, so a resumed export first adopts the transaction whose custom_data
// names it.
func (p paddleProvider) push(ctx context.Context, conn connection, export *state.StatementInvoiceExport, src statementInvoiceSource,
	resumed bool, record func(string) error, reject func()) *api.Problem {
	if export.ProviderInvoiceID != "" {
		return p.collect(ctx, conn, export)
	}
	items, problem := nonNegativeItems(src)
	if problem != nil {
		reject()
		return problem
	}
	id := ""
	if resumed {
		found, err := p.client.FindTransaction(ctx, conn.key, export.Customer, export.ID)
		if err != nil {
			return paddleCallProblem(err)
		}
		id = found
	}
	if id == "" {
		params := paddleinvoice.TransactionParams{CustomerID: export.Customer, Currency: export.Currency,
			CustomData: map[string]string{"gregale_export_id": export.ID, "gregale_statement_id": src.statementID,
				"gregale_statement_revision": strconv.Itoa(src.revision), "gregale_source": src.kind}}
		if export.Collection == state.CollectionSend {
			params.PaymentTermsDays = export.DaysUntilDue
		}
		for _, item := range items {
			params.Items = append(params.Items, paddleinvoice.Item{Description: item.description, AmountMinor: item.amount})
		}
		created, err := p.client.CreateTransaction(ctx, conn.key, params)
		if err != nil {
			if paddleRefused(err) {
				reject()
			}
			return paddleCallProblem(err)
		}
		id = created
	}
	if err := record(id); err != nil {
		return api.ErrInternal("could not record the Paddle transaction")
	}
	return p.collect(ctx, conn, export)
}

// collect bills the transaction of an export set to send
// (ADR-20261019100333): Paddle numbers the invoice and emails it. Only a
// draft or ready transaction is billed, so the step is safe to repeat.
func (p paddleProvider) collect(ctx context.Context, conn connection, export *state.StatementInvoiceExport) *api.Problem {
	if export.Collection != state.CollectionSend {
		return nil
	}
	txn, err := p.client.GetTransaction(ctx, conn.key, export.ProviderInvoiceID)
	if err != nil {
		return paddleCallProblem(err)
	}
	if txn.Deleted || txn.Status != "draft" && txn.Status != "ready" {
		return nil
	}
	address, err := p.client.CustomerAddress(ctx, conn.key, export.Customer)
	if errors.Is(err, paddleinvoice.ErrNoAddress) {
		return providerProblem(http.StatusUnprocessableEntity, "Paddle customer has no address",
			"add an address to the customer in Paddle, then export again; the transaction is kept as a draft")
	}
	if err != nil {
		return paddleCallProblem(err)
	}
	// The customer's business, when it has one, carries its tax identifier
	// onto the invoice (ADR-20261020120000).
	business, hasBusiness, err := p.client.ActiveBusiness(ctx, conn.key, export.Customer)
	if err != nil {
		return paddleCallProblem(err)
	}
	if !hasBusiness {
		business.ID = ""
	}
	if err := p.client.BillTransaction(ctx, conn.key, export.ProviderInvoiceID, address, business.ID); err != nil {
		return paddleCallProblem(err)
	}
	return nil
}

// paddleStatuses map Paddle transaction statuses to payment statuses.
var paddleStatuses = map[string]string{
	"draft": state.InvoicePaymentDraft, "ready": state.InvoicePaymentDraft,
	"billed": state.InvoicePaymentOpen, "past_due": state.InvoicePaymentOpen,
	"paid": state.InvoicePaymentPaid, "completed": state.InvoicePaymentPaid,
	"canceled": state.InvoicePaymentVoid,
}

func (p paddleProvider) payment(ctx context.Context, conn connection, id string) (state.StatementInvoicePayment, bool, error) {
	txn, err := p.client.GetTransaction(ctx, conn.key, id)
	if errors.Is(err, paddleinvoice.ErrKeyRejected) {
		return state.StatementInvoicePayment{}, false, fmt.Errorf("%w: %w", errKeyRejected, err)
	}
	if err != nil {
		return state.StatementInvoicePayment{}, false, err
	}
	if txn.Deleted {
		return state.StatementInvoicePayment{Status: state.InvoicePaymentDeleted}, true, nil
	}
	status, ok := paddleStatuses[txn.Status]
	if !ok {
		return state.StatementInvoicePayment{}, false, nil
	}
	remaining := max(txn.Balance, 0)
	if status == state.InvoicePaymentPaid {
		remaining = 0
	}
	return knownPayment(state.StatementInvoicePayment{Status: status, AmountPaidMinor: max(txn.GrandTotal-remaining, 0),
		AmountRemainingMinor: remaining, DueAt: txn.DueAt, PaidAt: txn.PaidAt}, txn.CheckoutURL)
}

// --- Lago (ADR-1507) ---

var lagoKeyRE = regexp.MustCompile(`^[A-Za-z0-9._-]{8,256}$`)

type lagoProvider struct{ client *lagoinvoice.Client }

func (lagoProvider) title() string                       { return "Lago" }
func (lagoProvider) currencyProblem(string) *api.Problem { return nil }
func (lagoProvider) dashboardURL(bool, string) string    { return "" }

// parseKey accepts Lago's opaque API keys; Lago has no test mode, so every
// connection is live.
func (lagoProvider) parseKey(key string) (bool, error) {
	if !lagoKeyRE.MatchString(key) {
		return false, errors.New("lago key must be 8 to 256 letters, digits, dots, dashes or underscores")
	}
	return true, nil
}

func (p lagoProvider) checkKey(ctx context.Context, conn connection) error {
	return p.client.CheckKey(ctx, conn.apiURL, conn.key)
}

func (lagoProvider) customer(requested, externalRef string) (string, *api.Problem) {
	return resolveCustomer(requested, externalRef, customerTextRE, "Lago",
		"pass customer as the Lago external customer ID, or set the external_ref to it")
}

func lagoCallProblem(err error) *api.Problem {
	var lagoErr *lagoinvoice.Error
	switch {
	case errors.Is(err, lagoinvoice.ErrKeyRejected):
		return providerProblem(http.StatusUnprocessableEntity, "Lago rejected the key", "use an API key of the Lago organization that bills the customer")
	case errors.As(err, &lagoErr) && lagoErr.Status >= 400 && lagoErr.Status < 500:
		return providerProblem(http.StatusUnprocessableEntity, "Lago rejected the request", lagoErr.Code+" "+lagoErr.Detail)
	default:
		return providerProblem(http.StatusBadGateway, "Could not reach Lago", "retry; an export resumes the same invoice")
	}
}

// push issues a one-off invoice once and tags it with the export. A resumed
// export adopts the tagged invoice; if an untagged one with the same fees
// appeared since the export began, an earlier attempt may have crashed
// before tagging it, so the export stops instead of issuing a second one.
func (p lagoProvider) push(ctx context.Context, conn connection, export *state.StatementInvoiceExport, src statementInvoiceSource,
	resumed bool, record func(string) error, reject func()) *api.Problem {
	if export.ProviderInvoiceID == "" {
		items, problem := nonNegativeItems(src)
		if problem != nil {
			reject()
			return problem
		}
		id, problem := p.findOrCreate(ctx, conn, export, src, items, resumed, reject)
		if problem != nil {
			return problem
		}
		if err := record(id); err != nil {
			return api.ErrInternal("could not record the Lago invoice")
		}
	}
	if err := p.client.SetExportMetadata(ctx, conn.apiURL, conn.key, export.ProviderInvoiceID, export.ID, src.statementID); err != nil {
		return lagoCallProblem(err)
	}
	return nil
}

func (p lagoProvider) findOrCreate(ctx context.Context, conn connection, export *state.StatementInvoiceExport, src statementInvoiceSource,
	items []statementInvoiceItem, resumed bool, reject func()) (string, *api.Problem) {
	if resumed {
		id, err := p.client.FindInvoice(ctx, conn.apiURL, conn.key, export.ID)
		if err != nil || id != "" {
			return id, callProblemOrNil(err, lagoCallProblem)
		}
		untagged, err := p.client.FindUntagged(ctx, conn.apiURL, conn.key, export.Customer, export.Currency, export.CreatedAt.AddDate(0, 0, -1), src.invoice.TotalMinor)
		if err != nil {
			return "", lagoCallProblem(err)
		}
		if len(untagged) > 0 {
			return "", exportConflict(fmt.Sprintf("Lago invoice %s may come from an earlier attempt of this export; void it in Lago, then retry", untagged[0]))
		}
	}
	if err := p.client.EnsureAddOn(ctx, conn.apiURL, conn.key, export.Currency); err != nil {
		return "", lagoCallProblem(err)
	}
	params := lagoinvoice.InvoiceParams{ExternalCustomerID: export.Customer, Currency: export.Currency, From: src.start, To: src.end,
		Charge: export.Collection == state.CollectionCharge}
	for _, item := range items {
		params.Fees = append(params.Fees, lagoinvoice.Fee{Description: item.description, AmountCents: item.amount})
	}
	id, err := p.client.CreateInvoice(ctx, conn.apiURL, conn.key, params)
	if err != nil {
		if lagoRefused(err) {
			reject()
		}
		return "", lagoCallProblem(err)
	}
	return id, nil
}

func callProblemOrNil(err error, problem func(error) *api.Problem) *api.Problem {
	if err == nil {
		return nil
	}
	return problem(err)
}

func (p lagoProvider) payment(ctx context.Context, conn connection, id string) (state.StatementInvoicePayment, bool, error) {
	inv, err := p.client.GetInvoice(ctx, conn.apiURL, conn.key, id)
	if errors.Is(err, lagoinvoice.ErrKeyRejected) {
		return state.StatementInvoicePayment{}, false, fmt.Errorf("%w: %w", errKeyRejected, err)
	}
	if err != nil {
		return state.StatementInvoicePayment{}, false, err
	}
	var status string
	switch {
	case inv.Deleted:
		return state.StatementInvoicePayment{Status: state.InvoicePaymentDeleted}, true, nil
	case inv.Status == "voided":
		status = state.InvoicePaymentVoid
	case inv.Status == "finalized" && inv.PaymentStatus == "succeeded":
		status = state.InvoicePaymentPaid
	case inv.Status == "finalized":
		status = state.InvoicePaymentOpen
	case inv.Status == "draft", inv.Status == "pending", inv.Status == "failed":
		status = state.InvoicePaymentDraft
	default:
		return state.StatementInvoicePayment{}, false, nil
	}
	return knownPayment(state.StatementInvoicePayment{Status: status, AmountPaidMinor: max(inv.PaidCents, 0),
		AmountRemainingMinor: max(inv.DueCents, 0), DueAt: inv.DueAt, PaidAt: inv.PaidAt}, inv.WebURL)
}

// knownPayment attaches a safe hosted URL and reports whether the status is
// one Gregale tracks.
func knownPayment(p state.StatementInvoicePayment, hostedURL string) (state.StatementInvoicePayment, bool, error) {
	if strings.HasPrefix(hostedURL, "https://") && len(hostedURL) <= 2048 {
		p.HostedInvoiceURL = hostedURL
	}
	for _, known := range state.InvoicePaymentStatuses {
		if p.Status == known && known != state.InvoicePaymentDeleted {
			return p, true, nil
		}
	}
	return p, false, nil
}
