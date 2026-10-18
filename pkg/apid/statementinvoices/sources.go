package statementinvoices

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

func notFinalizedProblem() *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeConflict, "Statement not finalized",
		"only finalized statements can be exported as invoices")
}

// ExportAppConsumerStatement exports a finalized app consumer statement as
// an invoice in the account's billing provider (ADR-1502, ADR-1507).
func (s *Service) ExportAppConsumerStatement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	exports, ok := s.statementInvoiceStore(w, acct)
	if !ok {
		return
	}
	src, providerName, problem := s.apiConsumerStatementInvoiceSource(w, r, acct)
	if src == nil {
		if problem != nil {
			api.WriteProblem(w, problem)
		}
		return
	}
	export, problem := s.exportAppConsumer(r.Context(), acct, exports, *src, providerName)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, statementInvoiceExportResponse(export))
}

// ExportAppConsumer exports a finalized app consumer statement to provider
// without an HTTP request; billing runs (ADR-1503) use it. An empty customer
// uses the consumer's external_ref.
func (s *Service) ExportAppConsumer(ctx context.Context, acct state.Account, consumer state.APIConsumer,
	statement state.APIConsumerUsageStatement, provider, customer string) (state.StatementInvoiceExport, *api.Problem) {
	exports, ok := s.Host.Store().(state.StatementInvoiceExportStore)
	if !ok {
		return state.StatementInvoiceExport{}, api.ErrInternal("statement invoice exports are unavailable")
	}
	src, problem := s.appConsumerInvoiceSource(ctx, acct, consumer, statement, customer)
	if problem != nil {
		return state.StatementInvoiceExport{}, problem
	}
	return s.exportAppConsumer(ctx, acct, exports, src, provider)
}

func (s *Service) exportAppConsumer(ctx context.Context, acct state.Account, exports state.StatementInvoiceExportStore,
	src statementInvoiceSource, provider string) (state.StatementInvoiceExport, *api.Problem) {
	export, problem := s.exportStatementInvoice(ctx, acct, exports, src, provider)
	if problem != nil {
		return export, problem
	}
	s.Host.Audit(ctx, "api_consumer_usage_statement.invoice_exported", acct.ID, map[string]any{"provider": export.Provider,
		"statement_id": export.StatementID, "invoice_id": export.ProviderInvoiceID, "livemode": export.Livemode})
	return export, nil
}

// apiConsumerStatementInvoiceSource loads a finalized app consumer
// statement, prices its invoice lines and reads the target provider. A nil
// source with a nil problem means a response was already written.
func (s *Service) apiConsumerStatementInvoiceSource(w http.ResponseWriter, r *http.Request, acct state.Account) (*statementInvoiceSource, string, *api.Problem) {
	app, consumer, statements, ok := s.Host.ConsumerStatements(w, r, acct)
	if !ok {
		return nil, "", nil
	}
	statement, err := statements.GetAPIConsumerUsageStatement(r.Context(), acct.ID, app.ID, consumer.ID, r.PathValue("statement_id"))
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no such usage statement")
		return nil, "", nil
	}
	if err != nil {
		return nil, "", api.ErrInternal("could not load API consumer usage statement")
	}
	if statement.Status != state.APIConsumerUsageStatementFinalized {
		return nil, "", notFinalizedProblem()
	}
	req, problem := decodeStatementInvoiceRequest(r)
	if problem != nil {
		return nil, "", problem
	}
	src, problem := s.appConsumerInvoiceSource(r.Context(), acct, consumer, statement, req.Customer)
	if problem != nil {
		return nil, "", problem
	}
	return &src, req.Provider, nil
}

// appConsumerInvoiceSource prices a finalized app consumer statement's
// invoice lines and binds its handoff.
func (s *Service) appConsumerInvoiceSource(ctx context.Context, acct state.Account, consumer state.APIConsumer,
	statement state.APIConsumerUsageStatement, requestedCustomer string) (statementInvoiceSource, *api.Problem) {
	handoffs, okHandoffs := s.Host.Store().(state.APIConsumerUsageStatementHandoffStore)
	cards, okCards := s.Host.Store().(state.APIConsumerRateCardStore)
	if !okHandoffs || !okCards {
		return statementInvoiceSource{}, api.ErrInternal("API consumer usage statements are unavailable")
	}
	if statement.Status != state.APIConsumerUsageStatementFinalized {
		return statementInvoiceSource{}, notFinalizedProblem()
	}
	appCards, err := cards.ListAPIConsumerRateCardsForApp(ctx, acct.ID, statement.AppID)
	if err != nil {
		return statementInvoiceSource{}, api.ErrInternal("could not load API consumer rate cards")
	}
	invoice, err := billing.AppStatementInvoice(statement, appCards)
	if err != nil {
		return statementInvoiceSource{}, statementInvoiceProblem(err)
	}
	src := statementInvoiceSource{kind: state.StatementSourceAppConsumer, statementID: statement.ID, revision: statement.Revision,
		start: statement.PeriodStart, end: statement.PeriodEnd, requestedCustomer: requestedCustomer, externalRef: consumer.ExternalRef, invoice: invoice}
	if handoff, err := handoffs.GetAPIConsumerUsageStatementHandoff(ctx, acct.ID, statement.AppID, statement.ConsumerID, statement.ID); err == nil {
		src.existingHandoff = handoff.ExternalInvoiceID
	}
	src.handoff = func(ctx context.Context, invoiceID string) error {
		_, _, err := handoffs.CreateAPIConsumerUsageStatementHandoff(ctx, state.APIConsumerUsageStatementHandoffInput{
			AccountID: acct.ID, AppID: statement.AppID, ConsumerID: statement.ConsumerID, StatementID: statement.ID, ExternalInvoiceID: invoiceID})
		return err
	}
	return src, nil
}

func (s *Service) GetAppConsumerStatementExport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	exports, ok := s.statementInvoiceStore(w, acct)
	if !ok {
		return
	}
	app, consumer, statements, ok := s.Host.ConsumerStatements(w, r, acct)
	if !ok {
		return
	}
	if _, err := statements.GetAPIConsumerUsageStatement(r.Context(), acct.ID, app.ID, consumer.ID, r.PathValue("statement_id")); err != nil {
		httpkit.NotFound(w, "no such usage statement")
		return
	}
	s.writeStatementInvoiceExport(w, r, acct, exports, state.StatementSourceAppConsumer)
}

func (s *Service) writeStatementInvoiceExport(w http.ResponseWriter, r *http.Request, acct state.Account, exports state.StatementInvoiceExportStore, kind string) {
	export, err := exports.GetStatementInvoiceExport(r.Context(), acct.ID, kind, r.PathValue("statement_id"))
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "statement has not been exported")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the statement invoice export"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, statementInvoiceExportResponse(export))
}

// ExportPlatformTenantStatement exports a finalized cross-app statement as
// an invoice in the account's billing provider (ADR-1502, ADR-1507).
func (s *Service) ExportPlatformTenantStatement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	exports, ok := s.statementInvoiceStore(w, acct)
	if !ok {
		return
	}
	tenant, statements, ok := s.Host.TenantStatements(w, r, acct)
	if !ok {
		return
	}
	statement, err := statements.GetPlatformTenantStatement(r.Context(), acct.ID, tenant.ID, r.PathValue("statement_id"))
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no such platform tenant statement")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load platform tenant statement"))
		return
	}
	if statement.Status != state.APIConsumerUsageStatementFinalized {
		api.WriteProblem(w, notFinalizedProblem())
		return
	}
	req, problem := decodeStatementInvoiceRequest(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	export, problem := s.exportPlatformTenant(r.Context(), acct, exports, tenant, statement, statements, req.Provider, req.Customer)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, statementInvoiceExportResponse(export))
}

// ExportPlatformTenant exports a finalized platform tenant statement to
// provider without an HTTP request; billing runs (ADR-1503) use it. An empty
// customer uses the tenant's external_ref.
func (s *Service) ExportPlatformTenant(ctx context.Context, acct state.Account, tenant state.PlatformTenant,
	statement state.PlatformTenantStatement, provider, customer string) (state.StatementInvoiceExport, *api.Problem) {
	exports, okExports := s.Host.Store().(state.StatementInvoiceExportStore)
	statements, okStatements := s.Host.Store().(state.PlatformTenantStatementStore)
	if !okExports || !okStatements {
		return state.StatementInvoiceExport{}, api.ErrInternal("statement invoice exports are unavailable")
	}
	return s.exportPlatformTenant(ctx, acct, exports, tenant, statement, statements, provider, customer)
}

func (s *Service) exportPlatformTenant(ctx context.Context, acct state.Account, exports state.StatementInvoiceExportStore,
	tenant state.PlatformTenant, statement state.PlatformTenantStatement, statements state.PlatformTenantStatementStore,
	provider, customer string) (state.StatementInvoiceExport, *api.Problem) {
	src, problem := tenantStatementInvoiceSource(ctx, acct, tenant, statement, statements, customer)
	if problem != nil {
		return state.StatementInvoiceExport{}, problem
	}
	export, problem := s.exportStatementInvoice(ctx, acct, exports, src, provider)
	if problem != nil {
		return export, problem
	}
	s.Host.Audit(ctx, "platform_tenant_statement.invoice_exported", acct.ID, map[string]any{"provider": export.Provider,
		"tenant_id": tenant.ID, "statement_id": export.StatementID, "invoice_id": export.ProviderInvoiceID, "livemode": export.Livemode})
	return export, nil
}

func tenantStatementInvoiceSource(ctx context.Context, acct state.Account, tenant state.PlatformTenant, statement state.PlatformTenantStatement,
	statements state.PlatformTenantStatementStore, requestedCustomer string) (statementInvoiceSource, *api.Problem) {
	if statement.Status != state.APIConsumerUsageStatementFinalized {
		return statementInvoiceSource{}, notFinalizedProblem()
	}
	// Tenant cards name each line's unit (ADR-1319).
	var cards []state.PlatformTenantRateCard
	if cardStore, ok := statements.(state.PlatformTenantRateCardStore); ok {
		var err error
		if cards, err = cardStore.ListPlatformTenantRateCards(ctx, acct.ID, tenant.ID); err != nil {
			return statementInvoiceSource{}, api.ErrInternal("could not load platform tenant rate cards")
		}
	}
	invoice, err := billing.TenantStatementInvoice(statement, cards...)
	if err != nil {
		return statementInvoiceSource{}, statementInvoiceProblem(err)
	}
	src := statementInvoiceSource{kind: state.StatementSourcePlatformTenant, statementID: statement.ID, revision: statement.Revision,
		start: statement.PeriodStart, end: statement.PeriodEnd, requestedCustomer: requestedCustomer, externalRef: tenant.ExternalRef, invoice: invoice}
	if handoff, err := statements.GetPlatformTenantStatementHandoff(ctx, acct.ID, tenant.ID, statement.ID); err == nil {
		src.existingHandoff = handoff.ExternalInvoiceID
	}
	src.handoff = func(ctx context.Context, invoiceID string) error {
		_, _, err := statements.CreatePlatformTenantStatementHandoff(ctx, state.PlatformTenantStatementHandoffInput{
			AccountID: acct.ID, TenantID: tenant.ID, StatementID: statement.ID, ExternalInvoiceID: invoiceID})
		return err
	}
	return src, nil
}

func (s *Service) GetPlatformTenantStatementExport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	exports, ok := s.statementInvoiceStore(w, acct)
	if !ok {
		return
	}
	tenant, statements, ok := s.Host.TenantStatements(w, r, acct)
	if !ok {
		return
	}
	if _, err := statements.GetPlatformTenantStatementHeader(r.Context(), acct.ID, tenant.ID, r.PathValue("statement_id")); err != nil {
		httpkit.NotFound(w, "no such platform tenant statement")
		return
	}
	s.writeStatementInvoiceExport(w, r, acct, exports, state.StatementSourcePlatformTenant)
}
