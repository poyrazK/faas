// Package statementinvoices exports finalized usage statements to the
// account's own Stripe as draft invoices (ADR-1300). Like the other apid
// domains (ADR-982), it reaches apid only through Host; apidserver keeps the
// route table.
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
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// stripeKeyMaxBytes bounds a stored Stripe key; real keys are ~110 bytes.
const stripeKeyMaxBytes = 512

var stripeCustomerIDRE = regexp.MustCompile(`^cus_[A-Za-z0-9]{1,250}$`)

func stripeKeyNamespace(accountID string) string { return "gregale.billing.stripe." + accountID }

// Host is what statement invoice exports need from apid (ADR-982).
type Host interface {
	Store() state.Store
	// ConsumerFeatureAllowed reports whether the plan includes API
	// consumers; on false it has already written the problem.
	ConsumerFeatureAllowed(w http.ResponseWriter, acct state.Account) bool
	// ConsumerStatements resolves the app, consumer and statement store of
	// an app consumer statement route; on false it has written the problem.
	ConsumerStatements(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.APIConsumer, state.APIConsumerUsageStatementStore, bool)
	// TenantStatements resolves the platform tenant and statement store of
	// a tenant statement route; on false it has written the problem.
	TenantStatements(w http.ResponseWriter, r *http.Request, acct state.Account) (state.PlatformTenant, state.PlatformTenantStatementStore, bool)
	Audit(ctx context.Context, event, accountID string, fields map[string]any)
	// SealSecret and OpenSecret seal with apid's own age identity.
	SealSecret(namespace string, plaintext []byte, maxBytes int) ([]byte, error)
	OpenSecret(blob []byte) (namespace string, plaintext []byte, err error)
}

// Service exports finalized usage statements to the account's own Stripe.
// Stripe is the API client; nil uses api.stripe.com.
type Service struct {
	Host   Host
	Stripe *stripeinvoice.Client
}

func (s *Service) stripeInvoiceClient() *stripeinvoice.Client {
	if s.Stripe != nil {
		return s.Stripe
	}
	return stripeinvoice.New()
}

func (s *Service) statementInvoiceStore(w http.ResponseWriter, acct state.Account) (state.StatementInvoiceExportStore, bool) {
	if !s.Host.ConsumerFeatureAllowed(w, acct) {
		return nil, false
	}
	store, ok := s.Host.Store().(state.StatementInvoiceExportStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("statement invoice exports are unavailable"))
		return nil, false
	}
	return store, true
}

func billingConnectionResponse(c state.BillingConnection) api.BillingConnectionResponse {
	return api.BillingConnectionResponse{Provider: c.Provider, KeyLast4: c.KeyLast4, Livemode: c.Livemode,
		CreatedAt: c.CreatedAt.UTC(), UpdatedAt: c.UpdatedAt.UTC()}
}

func stripeConnectionProblem(status int, title, detail string) *api.Problem {
	code := api.CodeValidation
	if status == http.StatusBadGateway {
		code = api.CodeBillingProviderUnavailable
	}
	return api.NewProblem(status, code, title, detail)
}

// PutStripeConnection checks the key against Stripe, seals it, and stores
// it; only its last four characters and mode are ever returned (ADR-1300).
func (s *Service) PutStripeConnection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.statementInvoiceStore(w, acct)
	if !ok {
		return
	}
	var req api.PutStripeConnectionRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	key := strings.TrimSpace(req.SecretKey)
	livemode, err := stripeinvoice.KeyLivemode(key)
	if err != nil || len(key) > stripeKeyMaxBytes {
		api.WriteProblem(w, stripeConnectionProblem(http.StatusUnprocessableEntity, "Invalid Stripe key", stripeinvoice.ErrKeyFormat.Error()))
		return
	}
	if err := s.stripeInvoiceClient().CheckKey(r.Context(), key); err != nil {
		api.WriteProblem(w, stripeCallProblem(err))
		return
	}
	sealed, err := s.Host.SealSecret(stripeKeyNamespace(acct.ID), []byte(key), stripeKeyMaxBytes)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not store the Stripe key"))
		return
	}
	conn, err := store.PutBillingConnection(r.Context(), state.BillingConnection{AccountID: acct.ID, Provider: state.BillingProviderStripe,
		SealedSecret: sealed, KeyLast4: key[len(key)-4:], Livemode: livemode})
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not store the Stripe key"))
		return
	}
	s.Host.Audit(r.Context(), "billing_connection.stripe_updated", acct.ID, map[string]any{"key_last4": conn.KeyLast4, "livemode": conn.Livemode})
	httpkit.WriteJSON(w, http.StatusOK, billingConnectionResponse(conn))
}

func (s *Service) GetStripeConnection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.statementInvoiceStore(w, acct)
	if !ok {
		return
	}
	conn, err := store.GetBillingConnection(r.Context(), acct.ID, state.BillingProviderStripe)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no Stripe connection")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the Stripe connection"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, billingConnectionResponse(conn))
}

func (s *Service) DeleteStripeConnection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.statementInvoiceStore(w, acct)
	if !ok {
		return
	}
	err := store.DeleteBillingConnection(r.Context(), acct.ID, state.BillingProviderStripe)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no Stripe connection")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not remove the Stripe connection"))
		return
	}
	s.Host.Audit(r.Context(), "billing_connection.stripe_deleted", acct.ID, map[string]any{})
	w.WriteHeader(http.StatusNoContent)
}

// stripeCallProblem explains a failed Stripe call without echoing the key.
func stripeCallProblem(err error) *api.Problem {
	var stripeErr *stripeinvoice.Error
	switch {
	case errors.Is(err, stripeinvoice.ErrKeyRejected):
		return stripeConnectionProblem(http.StatusUnprocessableEntity, "Stripe rejected the key",
			"use a secret or restricted key with read and write access to invoices and invoice items")
	case errors.As(err, &stripeErr) && stripeErr.Status >= 400 && stripeErr.Status < 500:
		return stripeConnectionProblem(http.StatusUnprocessableEntity, "Stripe rejected the request", stripeErr.Message)
	default:
		return stripeConnectionProblem(http.StatusBadGateway, "Could not reach Stripe",
			"retry; an export resumes the same draft invoice")
	}
}

// statementInvoiceCustomer resolves the Stripe customer: the request's, or
// the consumer's or tenant's external_ref when it is a Stripe customer ID.
func statementInvoiceCustomer(requested, externalRef string) (string, *api.Problem) {
	customer := strings.TrimSpace(requested)
	if customer == "" && stripeCustomerIDRE.MatchString(externalRef) {
		customer = externalRef
	}
	if !stripeCustomerIDRE.MatchString(customer) {
		return "", api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Stripe customer required",
			"pass customer as a Stripe customer ID (cus_…), or set the external_ref to one")
	}
	return customer, nil
}

// statementInvoiceSource is one finalized statement to export.
type statementInvoiceSource struct {
	kind            string
	statementID     string
	revision        int
	start, end      time.Time
	customer        string
	invoice         billing.StatementInvoice
	existingHandoff string
	handoff         func(ctx context.Context, invoiceID string) error
}

func statementInvoiceExportResponse(e state.StatementInvoiceExport) api.StatementInvoiceExportResponse {
	out := api.StatementInvoiceExportResponse{StatementID: e.StatementID, Provider: e.Provider, Customer: e.Customer,
		Status: e.Status, InvoiceID: e.ProviderInvoiceID, Currency: e.Currency, AmountMinor: e.AmountMinor,
		Livemode: e.Livemode, CreatedAt: e.CreatedAt.UTC(), CompletedAt: e.CompletedAt}
	if e.ProviderInvoiceID != "" {
		out.DashboardURL = stripeinvoice.DashboardURL(e.Livemode, e.ProviderInvoiceID)
	}
	return out
}

func exportConflict(detail string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "Statement invoice conflict", detail)
}

// exportStatementInvoice creates the statement's draft Stripe invoice, or
// resumes an earlier attempt: the export row exists before any Stripe call,
// the invoice and items use idempotency keys derived from it, and items
// already on the invoice are skipped, so a retry never duplicates charges.
func (s *Service) exportStatementInvoice(ctx context.Context, acct state.Account, store state.StatementInvoiceExportStore,
	src statementInvoiceSource) (state.StatementInvoiceExport, *api.Problem) {
	prior, err := store.GetStatementInvoiceExport(ctx, acct.ID, src.kind, src.statementID)
	if err == nil && prior.Status == state.StatementInvoiceExportCompleted {
		return prior, nil
	}
	if src.existingHandoff != "" && (err != nil || prior.ProviderInvoiceID != src.existingHandoff) {
		return state.StatementInvoiceExport{}, exportConflict("the statement was already handed off to another invoice")
	}
	if src.existingHandoff == "" {
		if problem := s.checkHandoffAllowed(ctx, acct, src); problem != nil {
			return state.StatementInvoiceExport{}, problem
		}
	}
	conn, err := store.GetBillingConnection(ctx, acct.ID, state.BillingProviderStripe)
	if errors.Is(err, state.ErrNotFound) {
		return state.StatementInvoiceExport{}, exportConflict("connect Stripe first: PUT /v1/account/billing-connections/stripe")
	}
	if err != nil {
		return state.StatementInvoiceExport{}, api.ErrInternal("could not load the Stripe connection")
	}
	key, problem := s.openStripeKey(acct.ID, conn)
	if problem != nil {
		return state.StatementInvoiceExport{}, problem
	}
	export, _, err := store.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{AccountID: acct.ID, SourceKind: src.kind,
		StatementID: src.statementID, Provider: state.BillingProviderStripe, Customer: src.customer,
		Currency: src.invoice.Currency, AmountMinor: src.invoice.TotalMinor, Livemode: conn.Livemode})
	if errors.Is(err, state.ErrConflict) {
		return state.StatementInvoiceExport{}, exportConflict("an export for this statement already targets another Stripe customer")
	}
	if err != nil {
		return state.StatementInvoiceExport{}, api.ErrInternal("could not record the statement invoice export")
	}
	if export.Livemode != conn.Livemode {
		return state.StatementInvoiceExport{}, exportConflict("the export started with a Stripe key in the other mode; reconnect that key to finish it")
	}
	if problem := s.pushStatementInvoice(ctx, key, &export, src); problem != nil {
		return state.StatementInvoiceExport{}, problem
	}
	if err := src.handoff(ctx, export.ProviderInvoiceID); err != nil {
		return state.StatementInvoiceExport{}, exportConflict("the statement was already handed off to another invoice")
	}
	done, err := store.CompleteStatementInvoiceExport(ctx, acct.ID, export.ID)
	if err != nil {
		return state.StatementInvoiceExport{}, api.ErrInternal("could not complete the statement invoice export")
	}
	return done, nil
}

// ErrHandoffOverlapDetail is the detail of the 409 an export returns when
// another statement already invoiced part of this statement's usage.
const ErrHandoffOverlapDetail = "another statement already handed off part of this usage to an invoice"

// checkHandoffAllowed refuses an export whose handoff ADR-238's overlap
// rules would reject, before any Stripe invoice exists (ADR-1301).
func (s *Service) checkHandoffAllowed(ctx context.Context, acct state.Account, src statementInvoiceSource) *api.Problem {
	checker, ok := s.Host.Store().(state.StatementHandoffChecker)
	if !ok {
		return nil
	}
	blocked, err := checker.StatementHandoffBlocked(ctx, acct.ID, src.kind, src.statementID)
	if err != nil {
		return api.ErrInternal("could not check the statement handoff")
	}
	if blocked {
		return exportConflict(ErrHandoffOverlapDetail)
	}
	return nil
}

func (s *Service) openStripeKey(accountID string, conn state.BillingConnection) (string, *api.Problem) {
	namespace, plain, err := s.Host.OpenSecret(conn.SealedSecret)
	if err != nil || namespace != stripeKeyNamespace(accountID) {
		return "", api.ErrInternal("could not open the stored Stripe key; reconnect Stripe")
	}
	return string(plain), nil
}

// pushStatementInvoice creates the draft invoice once and adds each line
// Stripe does not already hold.
func (s *Service) pushStatementInvoice(ctx context.Context, key string, export *state.StatementInvoiceExport, src statementInvoiceSource) *api.Problem {
	stripe := s.stripeInvoiceClient()
	store, ok := s.Host.Store().(state.StatementInvoiceExportStore)
	if !ok {
		return api.ErrInternal("statement invoice exports are unavailable")
	}
	metadata := map[string]string{"gregale_statement_id": src.statementID, "gregale_statement_revision": strconv.Itoa(src.revision), "gregale_source": src.kind}
	if export.ProviderInvoiceID == "" {
		description := fmt.Sprintf("Usage from %s to %s", src.start.UTC().Format(time.DateOnly), src.end.UTC().Format(time.DateOnly))
		if src.revision > 1 {
			description += fmt.Sprintf(" (adjustment %d)", src.revision)
		}
		invoice, err := stripe.CreateInvoice(ctx, key, "gregale-"+export.ID+"-invoice", stripeinvoice.InvoiceParams{
			Customer: export.Customer, Currency: export.Currency, Description: description, Metadata: metadata})
		if err != nil {
			return stripeCallProblem(err)
		}
		if err := store.SetStatementInvoiceExportInvoice(ctx, export.AccountID, export.ID, invoice.ID); err != nil {
			return api.ErrInternal("could not record the Stripe invoice")
		}
		export.ProviderInvoiceID = invoice.ID
	}
	existing, err := stripe.ExportedLines(ctx, key, export.ProviderInvoiceID)
	if err != nil {
		return stripeCallProblem(err)
	}
	for i, item := range statementInvoiceItems(src.invoice) {
		line := strconv.Itoa(i)
		if existing[line] {
			continue
		}
		lineMetadata := map[string]string{stripeinvoice.LineMetadataKey: line, "gregale_statement_id": src.statementID}
		if err := stripe.CreateInvoiceItem(ctx, key, "gregale-"+export.ID+"-item-"+line, stripeinvoice.ItemParams{
			Customer: export.Customer, InvoiceID: export.ProviderInvoiceID, Currency: export.Currency, AmountMinor: item.amount,
			Description: item.description, PeriodStart: src.start, PeriodEnd: src.end, Metadata: lineMetadata}); err != nil {
			return stripeCallProblem(err)
		}
	}
	return nil
}

type statementInvoiceItem struct {
	amount      int64
	description string
}

// statementInvoiceItems renders invoice lines, then the rounding item that
// makes the invoice equal the statement total rounded to the cent.
func statementInvoiceItems(inv billing.StatementInvoice) []statementInvoiceItem {
	items := make([]statementInvoiceItem, 0, len(inv.Lines)+1)
	for _, line := range inv.Lines {
		description := fmt.Sprintf("%d usage units at %s %s each", line.Units, formatMillicentsDecimal(line.PriceMillicentsPerUnit), inv.Currency)
		if line.PriceMillicentsPerUnit == 0 {
			description = fmt.Sprintf("%d usage units included at no charge", line.Units)
		}
		items = append(items, statementInvoiceItem{amount: line.AmountMinor, description: description})
	}
	if inv.RoundingMinor != 0 {
		items = append(items, statementInvoiceItem{amount: inv.RoundingMinor, description: "Rounding to the nearest cent"})
	}
	return items
}

// formatMillicentsDecimal renders 1/100000 currency units exactly, e.g.
// 125 as 0.00125 and 100000 as 1.00.
func formatMillicentsDecimal(millicents int64) string {
	fraction := strings.TrimRight(fmt.Sprintf("%05d", millicents%100000), "0")
	for len(fraction) < 2 {
		fraction += "0"
	}
	return fmt.Sprintf("%d.%s", millicents/100000, fraction)
}

func statementInvoiceProblem(err error) *api.Problem {
	switch {
	case errors.Is(err, billing.ErrInvoiceCurrencyUnsupported):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Currency not supported",
			"statement invoices support two-decimal currencies only")
	case errors.Is(err, billing.ErrStatementNotInvoiceable):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Statement cannot be invoiced", err.Error())
	default:
		return api.ErrInternal("could not prepare the statement invoice")
	}
}

func decodeStatementInvoiceRequest(r *http.Request) (api.CreateStatementInvoiceRequest, *api.Problem) {
	var req api.CreateStatementInvoiceRequest
	if r.ContentLength == 0 {
		return req, nil
	}
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		return req, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error())
	}
	return req, nil
}
