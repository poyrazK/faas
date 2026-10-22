package apidserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/billingruns"
	"github.com/onebox-faas/faas/pkg/apid/product"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// billingRunsHost adapts *server to billingruns.Host (ADR-982). Statement
// steps call the same functions as the statement endpoints, so a run
// applies identical rules, audit events and webhooks (ADR-1503).
type billingRunsHost struct{ s *server }

var _ billingruns.Host = billingRunsHost{}

func (h billingRunsHost) Store() state.Store { return h.s.store }

func (h billingRunsHost) ConsumerFeatureAllowed(w http.ResponseWriter, acct state.Account) bool {
	return h.s.consumerFeatureAllowed(w, acct)
}

func (h billingRunsHost) Audit(ctx context.Context, event, accountID string, fields map[string]any) {
	h.s.audit.Emit(ctx, event, &accountID, fields)
}

func (h billingRunsHost) consumerStatements() (state.APIConsumerUsageStatementStore, *api.Problem) {
	store, ok := h.s.store.(state.APIConsumerUsageStatementStore)
	if !ok {
		return nil, api.ErrInternal("API consumer usage statements are unavailable")
	}
	return store, nil
}

func (h billingRunsHost) tenantStatements() (state.PlatformTenantStatementStore, *api.Problem) {
	store, ok := h.s.store.(state.PlatformTenantStatementStore)
	if !ok {
		return nil, api.ErrInternal("platform tenant statements are unavailable")
	}
	return store, nil
}

func (h billingRunsHost) DraftConsumerStatement(ctx context.Context, acct state.Account, appID, consumerID string,
	start, end time.Time) (state.APIConsumerUsageStatement, *api.Problem) {
	store, problem := h.consumerStatements()
	if problem != nil {
		return state.APIConsumerUsageStatement{}, problem
	}
	statement, _, err := h.s.draftAPIConsumerStatement(ctx, acct, appID, consumerID, store, start, end)
	if err != nil {
		return statement, apiConsumerStatementDraftProblem(err)
	}
	return statement, nil
}

func (h billingRunsHost) FinalizeConsumerStatement(ctx context.Context, acct state.Account, appID, consumerID,
	statementID string) (state.APIConsumerUsageStatement, *api.Problem) {
	store, problem := h.consumerStatements()
	if problem != nil {
		return state.APIConsumerUsageStatement{}, problem
	}
	statement, err := h.s.finalizeAPIConsumerStatement(ctx, acct, appID, consumerID, store, statementID)
	if errors.Is(err, state.ErrNotFound) {
		return statement, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such usage statement")
	}
	if err != nil {
		return statement, apiConsumerStatementFinalizeProblem(err)
	}
	return statement, nil
}

func (h billingRunsHost) ConsumerCompleteness(ctx context.Context, acct state.Account, appID, consumerID string,
	start, end time.Time) (string, int64, error) {
	from, until, checkable := completenessWindow(start, end, time.Now())
	if !checkable {
		return billing.CompletenessUnverifiable, 0, nil
	}
	result, err := h.s.consumerUsageCompleteness(ctx, acct.ID, appID, consumerID, from, until)
	return result.Status, result.MissingRequests, err
}

func (h billingRunsHost) ExportConsumerStatement(ctx context.Context, acct state.Account, consumer state.APIConsumer,
	statement state.APIConsumerUsageStatement, provider string) (state.StatementInvoiceExport, *api.Problem) {
	return h.s.statementInvoices.ExportAppConsumer(ctx, acct, consumer, statement, provider, "")
}

func (h billingRunsHost) DraftTenantStatement(ctx context.Context, acct state.Account, tenantID string,
	start, end time.Time) (state.PlatformTenantStatement, *api.Problem) {
	store, problem := h.tenantStatements()
	if problem != nil {
		return state.PlatformTenantStatement{}, problem
	}
	statement, _, err := h.s.draftPlatformTenantStatement(ctx, acct, tenantID, store, start, end)
	if err != nil {
		return statement, tenantStatementProblem(err)
	}
	return statement, nil
}

func (h billingRunsHost) FinalizeTenantStatement(ctx context.Context, acct state.Account, tenantID,
	statementID string) (state.PlatformTenantStatement, *api.Problem) {
	store, problem := h.tenantStatements()
	if problem != nil {
		return state.PlatformTenantStatement{}, problem
	}
	statement, err := h.s.finalizeTenantStatement(ctx, acct, tenantID, store, statementID)
	if errors.Is(err, state.ErrNotFound) {
		return statement, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such platform tenant statement")
	}
	if err != nil {
		return statement, tenantStatementFinalizeProblem(err)
	}
	return statement, nil
}

func (h billingRunsHost) ExportTenantStatement(ctx context.Context, acct state.Account, tenant state.PlatformTenant,
	statement state.PlatformTenantStatement, provider string) (state.StatementInvoiceExport, *api.Problem) {
	return h.s.statementInvoices.ExportPlatformTenant(ctx, acct, tenant, statement, provider, "")
}

func (h billingRunsHost) ProductControlEnabled() bool { return h.s.productControl.Enabled }

func (h billingRunsHost) FinalizeProductStatement(ctx context.Context, acct state.Account, scope state.ProductBillingScope,
	reviewHash string) (api.ProductBillingDocument, *api.Problem) {
	doc, err := h.s.productControl.FinalizeStatement(ctx, acct, scope, reviewHash)
	if err != nil {
		return doc, product.BillingProblem(err)
	}
	return doc, nil
}

func (h billingRunsHost) ExportProductStatement(ctx context.Context, acct state.Account, scope state.ProductBillingScope,
	documentID, provider string) (api.StatementInvoiceExportResponse, *api.Problem) {
	return h.s.productControl.ExportStatement(ctx, acct, scope, documentID, provider, "")
}

func (h billingRunsHost) SendEmail(ctx context.Context, to, subject, body, messageID string) error {
	if h.s.mailer == nil || to == "" {
		return errors.New("send email: no mailer or recipient")
	}
	return h.s.mailer.Send(ctx, Message{To: []string{to}, Subject: subject, TextBody: body, MessageID: messageID})
}
