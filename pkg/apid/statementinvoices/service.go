// Package statementinvoices exports finalized usage statements as invoices
// in the account's own Stripe, Paddle or Lago (ADR-1502, ADR-1507). Like
// the other apid domains (ADR-982), it reaches apid only through Host;
// apidserver keeps the route table.
package statementinvoices

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/lagoinvoice"
	"github.com/onebox-faas/faas/pkg/billing/paddleinvoice"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// providerKeyMaxBytes bounds a stored provider key; real keys are ~110
// bytes.
const providerKeyMaxBytes = 512

func keyNamespace(provider, accountID string) string {
	return "gregale.billing." + provider + "." + accountID
}

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
	// SendEmail sends a plain-text notification (ADR-1504); messageID is the
	// provider idempotency key.
	SendEmail(ctx context.Context, to, subject, body, messageID string) error
}

// Service exports finalized usage statements to the account's own billing
// provider. Stripe, Paddle and Lago are the API clients; nil uses the real
// APIs, and Lago's default client refuses private addresses.
type Service struct {
	Host   Host
	Stripe *stripeinvoice.Client
	Paddle *paddleinvoice.Client
	Lago   *lagoinvoice.Client
	// Now is the payment sync clock; nil uses time.Now.
	Now func() time.Time
	// Enabled pauses the background worker while it reports false: API
	// monetization is released by a runtime setting (ADR-20261018090210).
	// Nil runs it.
	Enabled func() bool
	// Released reports whether API monetization is released to an account
	// (ADR-20261020170649); the worker skips accounts it is not. Nil
	// releases every account.
	Released func(accountID string) bool
}

// released reports whether the worker serves an account's monetization.
func (s *Service) released(accountID string) bool { return s.Released == nil || s.Released(accountID) }

// tick runs one background tick unless Enabled pauses the worker; paused,
// no provider reads and no overdue digests.
func (s *Service) tick(ctx context.Context) error {
	if s.Enabled != nil && !s.Enabled() {
		return nil
	}
	return s.SyncOnce(ctx)
}

func (s *Service) stripeInvoiceClient() *stripeinvoice.Client {
	if s.Stripe != nil {
		return s.Stripe
	}
	return stripeinvoice.New()
}

func (s *Service) paddleClient() *paddleinvoice.Client {
	if s.Paddle != nil {
		return s.Paddle
	}
	return paddleinvoice.New()
}

// lagoClient dials self-hosted Lago URLs through the egress guard, so a
// connection cannot reach loopback, private or metadata addresses.
func (s *Service) lagoClient() *lagoinvoice.Client {
	if s.Lago != nil {
		return s.Lago
	}
	hc := oci.NewEgressHTTPClientAllowLoopback()
	if hc == nil {
		hc = oci.NewEgressHTTPClient()
	}
	hc.Timeout = 20 * time.Second
	return &lagoinvoice.Client{HTTP: hc}
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
	return api.BillingConnectionResponse{Provider: c.Provider, KeyLast4: c.KeyLast4, Livemode: c.Livemode, APIURL: c.APIURL,
		Collection: c.Collection, DaysUntilDue: c.DaysUntilDue, AutomaticTax: c.AutomaticTax, CreatedAt: c.CreatedAt.UTC(), UpdatedAt: c.UpdatedAt.UTC()}
}

func unknownProviderProblem() *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Unknown billing provider", "billing providers are stripe, paddle and lago")
}

// connectionTarget resolves the {provider} path value; on false it has
// written the problem.
func (s *Service) connectionTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.StatementInvoiceExportStore, string, provider, bool) {
	name := r.PathValue("provider")
	p := s.provider(name)
	if p == nil {
		api.WriteProblem(w, unknownProviderProblem())
		return nil, "", nil, false
	}
	store, ok := s.statementInvoiceStore(w, acct)
	return store, name, p, ok
}

// PutBillingConnection checks the key against the provider, seals it, and
// stores it; only its last four characters, mode and Lago API URL are ever
// returned (ADR-1502, ADR-1507).
func (s *Service) PutBillingConnection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, name, p, ok := s.connectionTarget(w, r, acct)
	if !ok {
		return
	}
	var req api.PutBillingConnectionRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	conn, problem := parseConnection(name, p, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if err := p.checkKey(r.Context(), conn); err != nil {
		api.WriteProblem(w, s.callProblem(name, err))
		return
	}
	sealed, err := s.Host.SealSecret(keyNamespace(name, acct.ID), []byte(conn.key), providerKeyMaxBytes)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not store the "+p.title()+" key"))
		return
	}
	stored, err := store.PutBillingConnection(r.Context(), state.BillingConnection{AccountID: acct.ID, Provider: name,
		SealedSecret: sealed, KeyLast4: conn.key[len(conn.key)-4:], Livemode: conn.livemode, APIURL: conn.apiURL,
		Collection: conn.collection, DaysUntilDue: conn.daysUntilDue, AutomaticTax: conn.automaticTax})
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not store the "+p.title()+" key"))
		return
	}
	s.Host.Audit(r.Context(), "billing_connection.updated", acct.ID, map[string]any{"provider": name,
		"key_last4": stored.KeyLast4, "livemode": stored.Livemode, "api_url": stored.APIURL, "collection": stored.Collection,
		"automatic_tax": stored.AutomaticTax})
	httpkit.WriteJSON(w, http.StatusOK, billingConnectionResponse(stored))
}

func parseConnection(name string, p provider, req api.PutBillingConnectionRequest) (connection, *api.Problem) {
	key := strings.TrimSpace(req.SecretKey)
	livemode, err := p.parseKey(key)
	if err != nil || len(key) > providerKeyMaxBytes {
		detail := "invalid key format"
		if err != nil {
			detail = err.Error()
		}
		return connection{}, providerProblem(http.StatusUnprocessableEntity, "Invalid "+p.title()+" key", detail)
	}
	conn := connection{key: key, livemode: livemode}
	if conn.collection, conn.daysUntilDue, err = parseCollection(name, req); err != nil {
		return connection{}, providerProblem(http.StatusUnprocessableEntity, "Bad collection", err.Error())
	}
	if req.AutomaticTax && name != state.BillingProviderStripe {
		return connection{}, providerProblem(http.StatusUnprocessableEntity, "Bad automatic_tax", "automatic_tax applies to Stripe connections only")
	}
	conn.automaticTax = req.AutomaticTax
	if name != state.BillingProviderLago {
		if strings.TrimSpace(req.APIURL) != "" {
			return connection{}, providerProblem(http.StatusUnprocessableEntity, "Bad api_url", "api_url applies to Lago connections only")
		}
		return conn, nil
	}
	conn.apiURL, err = lagoinvoice.NormalizeURL(req.APIURL)
	if err != nil {
		return connection{}, providerProblem(http.StatusUnprocessableEntity, "Bad api_url", err.Error())
	}
	return conn, nil
}

// parseCollection checks a connection's collection mode (ADR-1311) against
// what its provider supports.
func parseCollection(name string, req api.PutBillingConnectionRequest) (string, int, error) {
	mode, days := strings.TrimSpace(req.Collection), req.DaysUntilDue
	if mode == "" {
		mode = state.CollectionNone
	}
	if !state.ValidCollection(name, mode) {
		return "", 0, fmt.Errorf("%s supports collection %s", name, strings.Join(state.ProviderCollectionModes[name], ", "))
	}
	if days == 0 {
		days = state.DefaultCollectionDaysUntilDue
	}
	if days < 1 || days > 365 || (req.DaysUntilDue != 0 && mode != state.CollectionSend) {
		return "", 0, fmt.Errorf("days_until_due is 1 to 365 and applies to collection send only")
	}
	return mode, days, nil
}

func (s *Service) GetBillingConnection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, name, p, ok := s.connectionTarget(w, r, acct)
	if !ok {
		return
	}
	conn, err := store.GetBillingConnection(r.Context(), acct.ID, name)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no "+p.title()+" connection")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the "+p.title()+" connection"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, billingConnectionResponse(conn))
}

func (s *Service) DeleteBillingConnection(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, name, p, ok := s.connectionTarget(w, r, acct)
	if !ok {
		return
	}
	err := store.DeleteBillingConnection(r.Context(), acct.ID, name)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no "+p.title()+" connection")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not remove the "+p.title()+" connection"))
		return
	}
	s.Host.Audit(r.Context(), "billing_connection.deleted", acct.ID, map[string]any{"provider": name})
	w.WriteHeader(http.StatusNoContent)
}

// callProblem explains a failed provider call without echoing the key.
func (s *Service) callProblem(name string, err error) *api.Problem {
	switch name {
	case state.BillingProviderPaddle:
		return paddleCallProblem(err)
	case state.BillingProviderLago:
		return lagoCallProblem(err)
	}
	return stripeCallProblem(err)
}

// statementInvoiceSource is one finalized statement to export. The customer
// is resolved against the target provider: requestedCustomer, else
// externalRef when it is one of the provider's customer IDs.
type statementInvoiceSource struct {
	kind              string
	statementID       string
	revision          int
	start, end        time.Time
	requestedCustomer string
	externalRef       string
	customer          string
	invoice           billing.StatementInvoice
	existingHandoff   string
	handoff           func(ctx context.Context, invoiceID string) error
}

func statementInvoiceExportResponse(e state.StatementInvoiceExport) api.StatementInvoiceExportResponse {
	out := api.StatementInvoiceExportResponse{StatementID: e.StatementID, Provider: e.Provider, Customer: e.Customer,
		Status: e.Status, InvoiceID: e.ProviderInvoiceID, Currency: e.Currency, AmountMinor: e.AmountMinor,
		Livemode: e.Livemode, Collection: e.Collection, AutomaticTax: e.AutomaticTax, CreatedAt: e.CreatedAt.UTC(), CompletedAt: e.CompletedAt,
		SourceKind: e.SourceKind, PaymentStatus: e.PaymentStatus, AmountPaidMinor: e.AmountPaidMinor,
		AmountRemainingMinor: e.AmountRemainingMinor, DueAt: e.DueAt, PaidAt: e.PaidAt,
		HostedInvoiceURL: e.HostedInvoiceURL, StatusSyncedAt: e.StatusSyncedAt, Overdue: e.Overdue(time.Now())}
	if e.ProviderInvoiceID != "" {
		out.DashboardURL = dashboardURL(e.Provider, e.Livemode, e.ProviderInvoiceID)
	}
	return out
}

func exportConflict(detail string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "Statement invoice conflict", detail)
}

func exportInProgress() *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeStatementInvoiceExportInProgress, "Statement invoice export in progress",
		"another attempt is exporting this statement; retry once it finishes")
}

// exportStatementInvoice creates the statement's invoice in the provider,
// or resumes an earlier attempt: the export row exists before any provider
// call, one attempt at a time claims it, and each provider adopts what an
// earlier attempt created, so a retry never duplicates charges
// (ADR-20261019180000). An export the provider refused before creating
// anything can be retried with another customer or provider.
func (s *Service) exportStatementInvoice(ctx context.Context, acct state.Account, store state.StatementInvoiceExportStore,
	src statementInvoiceSource, providerName string) (state.StatementInvoiceExport, *api.Problem) {
	p := s.provider(providerName)
	if p == nil {
		return state.StatementInvoiceExport{}, providerProblem(http.StatusUnprocessableEntity, "Unknown billing provider", "provider must be stripe, paddle or lago")
	}
	prior, err := store.GetStatementInvoiceExport(ctx, acct.ID, src.kind, src.statementID)
	if err == nil && prior.Provider != providerName && !prior.Retargetable() {
		return state.StatementInvoiceExport{}, exportConflict("the statement was already exported to " + prior.Provider)
	}
	if err == nil && prior.Status == state.StatementInvoiceExportCompleted {
		return prior, nil
	}
	if src.existingHandoff != "" && (err != nil || prior.ProviderInvoiceID != src.existingHandoff) {
		return state.StatementInvoiceExport{}, exportConflict("the statement was already handed off to another invoice")
	}
	customer, problem := p.customer(src.requestedCustomer, src.externalRef)
	if problem != nil {
		return state.StatementInvoiceExport{}, problem
	}
	src.customer = customer
	if problem := p.currencyProblem(src.invoice.Currency); problem != nil {
		return state.StatementInvoiceExport{}, problem
	}
	if src.existingHandoff == "" {
		if problem := s.checkHandoffAllowed(ctx, acct, src); problem != nil {
			return state.StatementInvoiceExport{}, problem
		}
	}
	conn, problem := s.openConnection(ctx, store, acct.ID, providerName, p)
	if problem != nil {
		return state.StatementInvoiceExport{}, problem
	}
	export, resumed, err := store.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{AccountID: acct.ID, SourceKind: src.kind,
		StatementID: src.statementID, Provider: providerName, Customer: src.customer,
		Currency: src.invoice.Currency, AmountMinor: src.invoice.TotalMinor, Livemode: conn.livemode,
		Collection: conn.collection, DaysUntilDue: conn.daysUntilDue, AutomaticTax: conn.automaticTax})
	if errors.Is(err, state.ErrConflict) {
		return state.StatementInvoiceExport{}, exportConflict("an export for this statement already targets another customer or provider")
	}
	if err != nil {
		return state.StatementInvoiceExport{}, api.ErrInternal("could not record the statement invoice export")
	}
	token := uuid.NewString()
	claimed, err := store.ClaimStatementInvoiceExport(ctx, acct.ID, export.ID, token, time.Now().Add(api.StatementInvoiceExportClaimTTL))
	if errors.Is(err, state.ErrStatementInvoiceExportInProgress) {
		return state.StatementInvoiceExport{}, exportInProgress()
	}
	if err != nil {
		return state.StatementInvoiceExport{}, api.ErrInternal("could not claim the statement invoice export")
	}
	defer func() { _ = store.ReleaseStatementInvoiceExport(context.WithoutCancel(ctx), acct.ID, export.ID, token) }()
	// The claim reads the export as the attempt before it left it.
	export = claimed
	if export.Status == state.StatementInvoiceExportCompleted {
		return export, nil
	}
	if export.Provider != providerName || export.Customer != src.customer {
		return state.StatementInvoiceExport{}, exportConflict("an export for this statement already targets another customer or provider")
	}
	if export.Livemode != conn.livemode {
		return state.StatementInvoiceExport{}, exportConflict("the export started with a " + p.title() + " key in the other mode; reconnect that key to finish it")
	}
	record := func(invoiceID string) error {
		if err := store.SetStatementInvoiceExportInvoice(ctx, export.AccountID, export.ID, invoiceID); err != nil {
			return err
		}
		export.ProviderInvoiceID = invoiceID
		return nil
	}
	rejected := false
	reject := func() { rejected = true }
	if problem := p.push(ctx, conn, &export, src, resumed, record, reject); problem != nil {
		if rejected && export.ProviderInvoiceID == "" {
			_ = store.RejectStatementInvoiceExport(context.WithoutCancel(ctx), acct.ID, export.ID, token)
		}
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
// rules would reject, before any provider invoice exists (ADR-1503).
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

func (s *Service) openConnection(ctx context.Context, store state.StatementInvoiceExportStore, accountID, name string, p provider) (connection, *api.Problem) {
	stored, err := store.GetBillingConnection(ctx, accountID, name)
	if errors.Is(err, state.ErrNotFound) {
		return connection{}, exportConflict(fmt.Sprintf("connect %s first: PUT /v1/account/billing-connections/%s", p.title(), name))
	}
	if err != nil {
		return connection{}, api.ErrInternal("could not load the " + p.title() + " connection")
	}
	key, ok := s.openKey(accountID, stored)
	if !ok {
		return connection{}, api.ErrInternal("could not open the stored " + p.title() + " key; reconnect " + p.title())
	}
	return connection{key: key, apiURL: stored.APIURL, livemode: stored.Livemode, collection: stored.Collection,
		daysUntilDue: stored.DaysUntilDue, automaticTax: stored.AutomaticTax}, nil
}

func (s *Service) openKey(accountID string, stored state.BillingConnection) (string, bool) {
	namespace, plain, err := s.Host.OpenSecret(stored.SealedSecret)
	if err != nil || namespace != keyNamespace(stored.Provider, accountID) {
		return "", false
	}
	return string(plain), true
}

type statementInvoiceItem struct {
	amount      int64
	description string
}

// statementInvoiceItems renders invoice lines, then the rounding item that
// makes the invoice equal the statement total rounded to the minor unit.
func statementInvoiceItems(inv billing.StatementInvoice) []statementInvoiceItem {
	items := make([]statementInvoiceItem, 0, len(inv.Lines)+1)
	for _, line := range inv.Lines {
		noun := unitNoun(line.Unit)
		description := fmt.Sprintf("%d %s at %s %s each", line.Units, noun, formatMillicentsDecimal(line.PriceMillicentsPerUnit), inv.Currency)
		switch {
		case line.Credit:
			description = "Prepaid credit applied"
		case line.Fee == state.StatementFeeBase:
			description = "Monthly base fee"
		case line.Fee == state.StatementFeeMinimum && line.AmountMillicents < 0:
			description = "Minimum monthly spend, reduced by later usage"
		case line.Fee == state.StatementFeeMinimum:
			description = "Minimum monthly spend top-up"
		case line.PriceMillicentsPerUnit == 0:
			description = fmt.Sprintf("%d %s included at no charge", line.Units, noun)
		}
		items = append(items, statementInvoiceItem{amount: line.AmountMinor, description: description})
	}
	if inv.RoundingMinor != 0 {
		items = append(items, statementInvoiceItem{amount: inv.RoundingMinor, description: "Rounding to the currency's smallest unit"})
	}
	return items
}

// unitNoun names an invoice line's units (ADR-1317, ADR-1318).
func unitNoun(unit string) string {
	switch unit {
	case state.APIConsumerRateCardUnitComputeSecond:
		return "compute seconds"
	case state.APIConsumerRateCardUnitResponseMegabyte:
		return "response megabytes"
	}
	if meter, ok := state.AppMeterOfUnit(unit); ok {
		return strings.ReplaceAll(meter, "_", " ")
	}
	return "usage units"
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
			"statement invoices support currencies with at most three decimal places")
	case errors.Is(err, billing.ErrStatementNotInvoiceable):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Statement cannot be invoiced", err.Error())
	default:
		return api.ErrInternal("could not prepare the statement invoice")
	}
}

// decodeStatementInvoiceRequest reads the optional body; an omitted
// provider is stripe.
func decodeStatementInvoiceRequest(r *http.Request) (api.CreateStatementInvoiceRequest, *api.Problem) {
	var req api.CreateStatementInvoiceRequest
	if r.ContentLength != 0 {
		if err := httpkit.DecodeJSON(r, &req); err != nil {
			return req, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error())
		}
	}
	if req.Provider == "" {
		req.Provider = state.BillingProviderStripe
	}
	if !state.ValidBillingProvider(req.Provider) {
		return req, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Unknown billing provider", "provider must be stripe, paddle or lago")
	}
	return req, nil
}
