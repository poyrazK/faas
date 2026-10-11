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

// ExportAppConsumerStatement exports a finalized app consumer
// statement as a draft invoice in the account's Stripe (ADR-1300).
func (s *Service) ExportAppConsumerStatement(w http.ResponseWriter, r *http.Request, acct state.Account) {
	exports, ok := s.statementInvoiceStore(w, acct)
	if !ok {
		return
	}
	src, problem := s.apiConsumerStatementInvoiceSource(w, r, acct)
	if src == nil {
		if problem != nil {
			api.WriteProblem(w, problem)
		}
		return
	}
	export, problem := s.exportStatementInvoice(r.Context(), acct, exports, *src)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.Host.Audit(r.Context(), "api_consumer_usage_statement.stripe_invoice_exported", acct.ID, map[string]any{
		"statement_id": export.StatementID, "invoice_id": export.ProviderInvoiceID, "livemode": export.Livemode})
	httpkit.WriteJSON(w, http.StatusOK, statementInvoiceExportResponse(export))
}

// apiConsumerStatementInvoiceSource loads a finalized app consumer
// statement and prices its invoice lines. A nil source with a nil problem
// means a response was already written.
func (s *Service) apiConsumerStatementInvoiceSource(w http.ResponseWriter, r *http.Request, acct state.Account) (*statementInvoiceSource, *api.Problem) {
	app, consumer, statements, ok := s.Host.ConsumerStatements(w, r, acct)
	if !ok {
		return nil, nil
	}
	handoffs, okHandoffs := s.Host.Store().(state.APIConsumerUsageStatementHandoffStore)
	cards, okCards := s.Host.Store().(state.APIConsumerRateCardStore)
	if !okHandoffs || !okCards {
		return nil, api.ErrInternal("API consumer usage statements are unavailable")
	}
	statement, err := statements.GetAPIConsumerUsageStatement(r.Context(), acct.ID, app.ID, consumer.ID, r.PathValue("statement_id"))
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no such usage statement")
		return nil, nil
	}
	if err != nil {
		return nil, api.ErrInternal("could not load API consumer usage statement")
	}
	if statement.Status != state.APIConsumerUsageStatementFinalized {
		return nil, notFinalizedProblem()
	}
	req, problem := decodeStatementInvoiceRequest(r)
	if problem != nil {
		return nil, problem
	}
	customer, problem := statementInvoiceCustomer(req.Customer, consumer.ExternalRef)
	if problem != nil {
		return nil, problem
	}
	appCards, err := cards.ListAPIConsumerRateCardsForApp(r.Context(), acct.ID, app.ID)
	if err != nil {
		return nil, api.ErrInternal("could not load API consumer rate cards")
	}
	invoice, err := billing.AppStatementInvoice(statement, appCards)
	if err != nil {
		return nil, statementInvoiceProblem(err)
	}
	src := &statementInvoiceSource{kind: state.StatementSourceAppConsumer, statementID: statement.ID, revision: statement.Revision,
		start: statement.PeriodStart, end: statement.PeriodEnd, customer: customer, invoice: invoice}
	if handoff, err := handoffs.GetAPIConsumerUsageStatementHandoff(r.Context(), acct.ID, app.ID, consumer.ID, statement.ID); err == nil {
		src.existingHandoff = handoff.ExternalInvoiceID
	}
	src.handoff = func(ctx context.Context, invoiceID string) error {
		_, _, err := handoffs.CreateAPIConsumerUsageStatementHandoff(ctx, state.APIConsumerUsageStatementHandoffInput{
			AccountID: acct.ID, AppID: app.ID, ConsumerID: consumer.ID, StatementID: statement.ID, ExternalInvoiceID: invoiceID})
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

// ExportPlatformTenantStatement exports a finalized cross-app
// statement as a draft invoice in the account's Stripe (ADR-1300).
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
	src, problem := tenantStatementInvoiceSource(r, acct, tenant, statement, statements)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	export, problem := s.exportStatementInvoice(r.Context(), acct, exports, src)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.Host.Audit(r.Context(), "platform_tenant_statement.stripe_invoice_exported", acct.ID, map[string]any{
		"tenant_id": tenant.ID, "statement_id": export.StatementID, "invoice_id": export.ProviderInvoiceID, "livemode": export.Livemode})
	httpkit.WriteJSON(w, http.StatusOK, statementInvoiceExportResponse(export))
}

func tenantStatementInvoiceSource(r *http.Request, acct state.Account, tenant state.PlatformTenant, statement state.PlatformTenantStatement,
	statements state.PlatformTenantStatementStore) (statementInvoiceSource, *api.Problem) {
	if statement.Status != state.APIConsumerUsageStatementFinalized {
		return statementInvoiceSource{}, notFinalizedProblem()
	}
	req, problem := decodeStatementInvoiceRequest(r)
	if problem != nil {
		return statementInvoiceSource{}, problem
	}
	customer, problem := statementInvoiceCustomer(req.Customer, tenant.ExternalRef)
	if problem != nil {
		return statementInvoiceSource{}, problem
	}
	invoice, err := billing.TenantStatementInvoice(statement)
	if err != nil {
		return statementInvoiceSource{}, statementInvoiceProblem(err)
	}
	src := statementInvoiceSource{kind: state.StatementSourcePlatformTenant, statementID: statement.ID, revision: statement.Revision,
		start: statement.PeriodStart, end: statement.PeriodEnd, customer: customer, invoice: invoice}
	if handoff, err := statements.GetPlatformTenantStatementHandoff(r.Context(), acct.ID, tenant.ID, statement.ID); err == nil {
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
