package product

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

const stripeSendBatchConfirmAction = "confirm_stripe_send_batch"

type stripeSendBatchItem struct {
	invoiceBatchStatement
	Hash  string    `json:"hash"`
	DueAt time.Time `json:"due_at"`
}

type stripeSendBatchConfirmation struct {
	Period   string                `json:"period"`
	Cursor   string                `json:"payment_cursor,omitempty"`
	Livemode bool                  `json:"livemode"`
	Items    []stripeSendBatchItem `json:"items"`
}

func (s *Service) canSendStripeBatch() bool {
	return s.ReviewStripeDraftSend != nil && s.SendStripeInvoice != nil
}

func (s *Service) OwnerStripeSendBatchReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	if !s.canSendStripeBatch() {
		s.ownerError(w, r, c, state.ErrNotFound)
		return
	}
	f, err := ownerParseFormFields(w, r, []string{"csrf_token", "period", "payment_cursor", "statement"}, "statement", api.ProductControlOwnerFormMaxBytes)
	if err == nil {
		err = s.ownerVerify(r, c, "review", acct.ID+"/"+c.app.ID)
	}
	if err == nil {
		err = s.reviewStripeSendBatch(w, r.Context(), c, f.Get("period"), f.Get("payment_cursor"), f["statement"])
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) OwnerStripeSendBatchConfirm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	if !s.canSendStripeBatch() {
		s.ownerError(w, r, c, state.ErrNotFound)
		return
	}
	f, err := ownerParseForm(w, r, []string{"csrf_token", "payload"})
	if err == nil {
		err = s.ownerVerify(r, c, stripeSendBatchConfirmAction, ownerConfirmationSubject(c, f.Get("payload")))
	}
	var batch stripeSendBatchConfirmation
	if err == nil {
		err = ownerDecodeReviewedPayload(f.Get("payload"), &batch)
	}
	if err == nil {
		err = batch.validate()
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	s.applyStripeSendBatch(w, r.Context(), c, batch)
	s.OwnerRender(w, r, acct, c.data)
}

func (s *Service) reviewStripeSendBatch(w http.ResponseWriter, parent context.Context, c *ownerContext, period, cursor string, selection []string) error {
	if !ownerClosedBillingPeriod(period) || len(cursor) > api.ProductControlOwnerCursorMaxBytes {
		return state.ErrInvalidArgument
	}
	statements, err := parseInvoiceBatchStatements(selection, api.ProductControlStripeSendBatchMax)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, ownerInvoiceBatchTimeout)
	defer cancel()
	batch := stripeSendBatchConfirmation{Period: period, Cursor: cursor}
	view := stripeSendBatchView(c, batch)
	for _, statement := range statements {
		scope := stripeSendBatchScope(c, period, statement.Tenant)
		row := stripeSendBatchRow(ctx, c, scope, statement.Document)
		if ctx.Err() != nil {
			row.Status = "Review timed out. Select this draft again."
		} else {
			review, problem := s.ReviewStripeDraftSend(ctx, c.account, scope, statement.Document)
			switch {
			case problem != nil:
				row.Status = stripeSendBatchProblem(problem)
			case len(batch.Items) > 0 && review.Livemode != batch.Livemode:
				row.Status = "This draft uses a different Stripe mode. Review it separately."
			default:
				batch.Livemode = review.Livemode
				row.InvoiceID, row.Recipient = review.InvoiceID, review.Recipient
				row.Amount = ownerMinor(review.Currency, review.AmountDueMinor)
				row.DueAt = review.DueAt.UTC().Format("2006-01-02 15:04 UTC")
				row.Status = "Ready to send"
				batch.Items = append(batch.Items, stripeSendBatchItem{invoiceBatchStatement: statement, Hash: review.Hash, DueAt: review.DueAt.UTC()})
			}
		}
		view.Rows = append(view.Rows, row)
	}
	if len(batch.Items) > 0 {
		view.Mode = ownerInvoiceMode(batch.Livemode)
		if err := s.setStripeSendBatchToken(w, c, view, batch); err != nil {
			return err
		}
	}
	c.data.StripeSendBatch = view
	return nil
}

func (s *Service) applyStripeSendBatch(w http.ResponseWriter, parent context.Context, c *ownerContext, batch stripeSendBatchConfirmation) {
	ctx, cancel := context.WithTimeout(parent, ownerInvoiceBatchTimeout)
	defer cancel()
	view := stripeSendBatchView(c, batch)
	view.Mode, view.Confirmed = ownerInvoiceMode(batch.Livemode), true
	retry := batch
	retry.Items = nil
	for _, item := range batch.Items {
		scope := stripeSendBatchScope(c, batch.Period, item.Tenant)
		row := stripeSendBatchRow(ctx, c, scope, item.Document)
		if ctx.Err() != nil {
			row.Status = "Batch paused. Retry to continue."
			retry.Items = append(retry.Items, item)
		} else {
			result, problem := s.SendStripeInvoice(ctx, c.account, scope, item.Document, item.Hash, item.DueAt)
			if problem == nil {
				row.InvoiceID, row.Status = result.InvoiceID, "Stripe accepted the send"
			} else {
				row.Status = stripeSendBatchProblem(problem)
				if ctx.Err() != nil || problem.Status >= 500 || problem.Status == http.StatusTooManyRequests ||
					problem.Code == api.CodeStatementInvoiceExportInProgress {
					retry.Items = append(retry.Items, item)
				}
			}
		}
		view.Rows = append(view.Rows, row)
	}
	if len(retry.Items) > 0 {
		if err := s.setStripeSendBatchToken(w, c, view, retry); err != nil {
			view.Error = "Reload Payment follow-up to continue unfinished sends."
		}
	}
	c.data.StripeSendBatch = view
}

func (s *Service) setStripeSendBatchToken(w http.ResponseWriter, c *ownerContext, view *dashboard.ProductControlStripeSendBatch, batch stripeSendBatchConfirmation) error {
	payload := ownerPayload(batch)
	token, err := s.ownerToken(w, c, stripeSendBatchConfirmAction, ownerConfirmationSubject(c, payload))
	if err != nil {
		return err
	}
	view.Payload, view.Token, view.Ready = payload, token, len(batch.Items)
	return nil
}

func stripeSendBatchScope(c *ownerContext, period, tenant string) state.ProductBillingScope {
	return state.ProductBillingScope{AccountID: c.account.ID, AppID: c.app.ID, TenantID: tenant, Period: period}
}

func stripeSendBatchView(c *ownerContext, batch stripeSendBatchConfirmation) *dashboard.ProductControlStripeSendBatch {
	queueURL := c.data.BaseURL
	if batch.Cursor != "" {
		queueURL = ownerPageURL(c, nil, "payment_cursor", batch.Cursor)
	}
	return &dashboard.ProductControlStripeSendBatch{Period: batch.Period, QueueURL: queueURL + "#payment-follow-up"}
}

func stripeSendBatchRow(ctx context.Context, c *ownerContext, scope state.ProductBillingScope, document string) dashboard.ProductControlStripeSendBatchRow {
	statement := invoiceBatchRow(ctx, c, scope, document)
	return dashboard.ProductControlStripeSendBatchRow{
		Name: statement.Name, TenantID: scope.TenantID, DocumentID: document, URL: statement.URL,
	}
}

func stripeSendBatchProblem(problem *api.Problem) string {
	if problem.Detail == "" {
		return problem.Title
	}
	if problem.Code == api.CodeBillingProviderUnavailable {
		return problem.Title + ". " + problem.Detail
	}
	return problem.Detail
}

func (batch stripeSendBatchConfirmation) validate() error {
	if !ownerClosedBillingPeriod(batch.Period) || len(batch.Cursor) > api.ProductControlOwnerCursorMaxBytes ||
		len(batch.Items) == 0 || len(batch.Items) > api.ProductControlStripeSendBatchMax {
		return state.ErrInvalidArgument
	}
	seen := make(map[string]bool, len(batch.Items))
	for _, item := range batch.Items {
		if !item.valid() || seen[item.Document] || !accessRevision(item.Hash) || item.DueAt.IsZero() {
			return state.ErrInvalidArgument
		}
		seen[item.Document] = true
	}
	return nil
}
