package product

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

// A batch covers one page. Stop accepting provider work before the console's
// response deadline so the owner can see partial results and retry.
const ownerInvoiceBatchTimeout = time.Minute

type invoiceBatchStatement struct {
	Tenant   string `json:"tenant"`
	Document string `json:"document"`
}

func (statement invoiceBatchStatement) valid() bool {
	return ownerUUID(statement.Tenant) && ownerUUID(statement.Document)
}

type invoiceBatchItem struct {
	invoiceBatchStatement
	Customer string `json:"customer"`
	Hash     string `json:"hash"`
}

type invoiceBatchConfirmation struct {
	Period   string             `json:"period"`
	Provider string             `json:"provider"`
	Items    []invoiceBatchItem `json:"items"`
}

func supportsInvoiceDrafts(provider string) bool {
	return provider == state.BillingProviderStripe || provider == state.BillingProviderPaddle
}

func (s *Service) ownerDraftInvoiceProviders(ctx context.Context, account string) ([]dashboard.ProductControlInvoiceProvider, error) {
	store, ok := s.Host.Store().(state.StatementInvoiceExportStore)
	if !ok {
		return nil, nil
	}
	var providers []dashboard.ProductControlInvoiceProvider
	for _, provider := range state.BillingProviders {
		if !supportsInvoiceDrafts(provider) {
			continue
		}
		conn, err := store.GetBillingConnection(ctx, account, provider)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if conn.Collection == state.CollectionNone {
			providers = append(providers, dashboard.ProductControlInvoiceProvider{ID: provider, Name: ownerInvoiceProvider(provider)})
		}
	}
	return providers, nil
}

func (s *Service) OwnerInvoiceBatchReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	f, err := ownerParseFormFields(w, r, []string{"csrf_token", "period", "provider", "statement"}, "statement", api.ProductControlOwnerFormMaxBytes)
	if err == nil {
		err = s.ownerVerify(r, c, "review", acct.ID+"/"+c.app.ID)
	}
	if err == nil {
		err = s.reviewInvoiceBatch(w, r.Context(), c, f.Get("period"), f.Get("provider"), f["statement"])
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) OwnerInvoiceBatchConfirm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	if !s.canReviewInvoice() {
		s.ownerError(w, r, c, state.ErrNotFound)
		return
	}
	f, err := ownerParseForm(w, r, []string{"csrf_token", "payload"})
	if err == nil {
		err = s.ownerVerify(r, c, "confirm_invoice_batch", ownerConfirmationSubject(c, f.Get("payload")))
	}
	var confirmation invoiceBatchConfirmation
	if err == nil {
		err = ownerDecodeReviewedPayload(f.Get("payload"), &confirmation)
	}
	if err == nil {
		err = confirmation.validate()
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	c.data.InvoiceBatch = s.applyInvoiceBatch(r.Context(), c, confirmation, f.Get("payload"), f.Get("csrf_token"))
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) reviewInvoiceBatch(w http.ResponseWriter, ctx context.Context, c *ownerContext, period, provider string, selection []string) error {
	if !s.canReviewInvoice() {
		return state.ErrNotFound
	}
	if !ownerClosedBillingPeriod(period) || !supportsInvoiceDrafts(provider) {
		return state.ErrInvalidArgument
	}
	statements, err := parseInvoiceBatchStatements(selection, ownerBillingBatchMaxWorkspaces)
	if err != nil {
		return err
	}
	view := invoiceBatchView(c, period, provider)
	confirmation := invoiceBatchConfirmation{Period: period, Provider: provider}
	for _, statement := range statements {
		scope := state.ProductBillingScope{AccountID: c.account.ID, AppID: c.app.ID, Period: period, TenantID: statement.Tenant}
		row := invoiceBatchRow(ctx, c, scope, statement.Document)
		review, problem := s.ReviewInvoice(ctx, c.account, scope, statement.Document, provider, "")
		switch {
		case problem != nil:
			row.Status = invoiceBatchProblem(problem)
		case review.Collection != state.CollectionNone:
			row.Status = "This export is set to collect payment. Open the statement to review its collection settings."
		default:
			row.Customer, row.Subtotal = review.Customer, ownerMinor(review.Currency, review.AmountMinor)
			row.Mode, row.AutomaticTax, row.Status = ownerInvoiceMode(review.Livemode), review.AutomaticTax, "Ready to export draft"
			confirmation.Items = append(confirmation.Items, invoiceBatchItem{invoiceBatchStatement: statement, Customer: review.Customer, Hash: review.Hash})
		}
		view.Rows = append(view.Rows, row)
	}
	view.Ready = len(confirmation.Items)
	if view.Ready > 0 {
		view.Payload = ownerPayload(confirmation)
		token, err := s.ownerToken(w, c, "confirm_invoice_batch", ownerConfirmationSubject(c, view.Payload))
		if err != nil {
			return err
		}
		view.Token = token
	}
	c.data.InvoiceBatch = view
	return nil
}

func (s *Service) applyInvoiceBatch(parent context.Context, c *ownerContext, confirmation invoiceBatchConfirmation, payload, token string) *dashboard.ProductControlInvoiceBatch {
	ctx, cancel := context.WithTimeout(parent, ownerInvoiceBatchTimeout)
	defer cancel()
	view := invoiceBatchView(c, confirmation.Period, confirmation.Provider)
	view.Confirmed = true
	for _, item := range confirmation.Items {
		scope := state.ProductBillingScope{AccountID: c.account.ID, AppID: c.app.ID, Period: confirmation.Period, TenantID: item.Tenant}
		row := invoiceBatchRow(ctx, c, scope, item.Document)
		row.Customer = item.Customer
		switch {
		case ctx.Err() != nil:
			row.Status = "Batch paused. Retry to continue."
			view.Payload, view.Token = payload, token
		default:
			value, problem := s.exportStatement(ctx, c.account, scope, item.Document, confirmation.Provider, item.Customer, item.Hash)
			if problem == nil {
				row.Subtotal, row.Mode, row.AutomaticTax = ownerMinor(value.Currency, value.AmountMinor), ownerInvoiceMode(value.Livemode), value.AutomaticTax
				row.Status = "Exported"
				row.InvoiceID = value.InvoiceID
			} else {
				row.Status = invoiceBatchProblem(problem)
				if problem.Status >= 500 || problem.Status == http.StatusTooManyRequests || problem.Code == api.CodeStatementInvoiceExportInProgress {
					view.Payload, view.Token = payload, token
				}
			}
		}
		view.Rows = append(view.Rows, row)
	}
	return view
}

func invoiceBatchView(c *ownerContext, period, provider string) *dashboard.ProductControlInvoiceBatch {
	return &dashboard.ProductControlInvoiceBatch{
		Period: period, Provider: ownerInvoiceProvider(provider),
		QueueURL: ownerInvoiceQueueURL(c, period), PaymentURL: c.data.BaseURL + "#payment-follow-up",
	}
}

func invoiceBatchRow(ctx context.Context, c *ownerContext, scope state.ProductBillingScope, document string) dashboard.ProductControlInvoiceBatchRow {
	row := dashboard.ProductControlInvoiceBatchRow{TenantID: scope.TenantID, DocumentID: document, URL: ownerStatementURL(c, scope, document)}
	if ctx.Err() == nil {
		if tenant, err := c.tenants.GetPlatformTenant(ctx, scope.AccountID, scope.TenantID); err == nil {
			row.Name = tenant.Name
		}
	}
	return row
}

func invoiceBatchProblem(problem *api.Problem) string {
	if problem.Status >= 500 && problem.Code != api.CodeBillingProviderUnavailable {
		return "Could not complete the export. Try again."
	}
	if problem.Detail != "" {
		return problem.Detail
	}
	return problem.Title
}

func (confirmation invoiceBatchConfirmation) validate() error {
	if !ownerClosedBillingPeriod(confirmation.Period) || !supportsInvoiceDrafts(confirmation.Provider) ||
		len(confirmation.Items) == 0 || len(confirmation.Items) > ownerBillingBatchMaxWorkspaces {
		return state.ErrInvalidArgument
	}
	seen := make(map[string]bool, len(confirmation.Items))
	for _, item := range confirmation.Items {
		if !item.valid() || seen[item.Document] || !accessRevision(item.Hash) ||
			!ownerText(item.Customer, api.ProductControlInvoiceReferenceMaxBytes) {
			return state.ErrInvalidArgument
		}
		seen[item.Document] = true
	}
	return nil
}

func parseInvoiceBatchStatements(selection []string, limit int) ([]invoiceBatchStatement, error) {
	if len(selection) == 0 || len(selection) > limit {
		return nil, state.ErrInvalidArgument
	}
	statements := make([]invoiceBatchStatement, 0, len(selection))
	seen := make(map[string]bool, len(selection))
	for _, selected := range selection {
		tenant, document, _ := strings.Cut(selected, "/")
		statement := invoiceBatchStatement{Tenant: tenant, Document: document}
		if !statement.valid() || seen[document] {
			return nil, state.ErrInvalidArgument
		}
		seen[document] = true
		statements = append(statements, statement)
	}
	return statements, nil
}
