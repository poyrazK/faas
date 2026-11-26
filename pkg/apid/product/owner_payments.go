package product

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ownerPaymentQueue(r *http.Request, c *ownerContext, q url.Values) error {
	store, ok := s.Host.Store().(state.ProductOwnerPaymentQueueStore)
	if !ok {
		return nil
	}
	now := s.clock()
	page, err := store.ListProductOwnerPaymentQueue(r.Context(), c.account.ID, c.app.ID, q.Get("payment_cursor"), now)
	if err != nil {
		return err
	}
	queue := &dashboard.ProductControlPaymentQueue{Cursor: q.Get("payment_cursor"), StripeBatchMax: api.ProductControlStripeSendBatchMax}
	lastBatchMonth := ""
	for _, item := range page.Items {
		row := s.ownerPaymentRow(c, item, now)
		row.CanBatchSendStripe = row.CanSendStripe && item.Action != state.ProductOwnerPaymentStripeSend && s.canSendStripeBatch()
		if row.CanBatchSendStripe && lastBatchMonth != item.Period {
			queue.StripeBatchMonths = append(queue.StripeBatchMonths, item.Period)
			lastBatchMonth = item.Period
		}
		queue.Items = append(queue.Items, row)
	}
	if next := ownerPageURL(c, q, "payment_cursor", page.NextCursor); next != "" {
		queue.NextURL = next + "#payment-follow-up"
	}
	c.data.PaymentQueue = queue
	return nil
}

func (s *Service) ownerPaymentRow(c *ownerContext, item state.ProductOwnerPaymentQueueItem, now time.Time) dashboard.ProductControlPaymentQueueItem {
	params := url.Values{"document": {item.DocumentID}}
	row := dashboard.ProductControlPaymentQueueItem{
		Name: item.Name, TenantID: item.TenantID, Period: item.Period, DocumentID: item.DocumentID,
		CanSendStripe: item.StripeSend && s.canSendStripeInvoice(),
		CanIssue:      item.PaddleDraft && s.canIssueInvoice(),
		Remaining:     "Unknown", DueAt: "—", LastSync: "Never",
		DocumentURL: ownerURL(c, item.TenantID, item.Period, params) + "#document-" + item.DocumentID,
		HostedURL:   api.ProviderHostedURL(item.HostedInvoiceURL),
	}
	switch item.Action {
	case state.ProductOwnerPaymentDraft:
		row.Action = "Draft to issue"
	case state.ProductOwnerPaymentStripeSend:
		row.Action = "Stripe send pending"
	case state.ProductOwnerPaymentStripeReview:
		row.Action = "Stripe send needs review"
		row.CanSendStripe = false
		row.CanCheckStripeSend = s.CheckStripeInvoiceSend != nil
		row.StripeDashboardURL = stripeinvoice.DashboardURL(item.Livemode, url.PathEscape(item.ProviderInvoiceID))
	case state.ProductOwnerPaymentOverdue:
		row.Action = "Overdue"
		row.CanRefresh = s.RefreshInvoicePayment != nil
	case state.ProductOwnerPaymentStale:
		row.Action = "Check payment status"
		row.CanRefresh = s.RefreshInvoicePayment != nil
	}
	reminderExpired := !item.ReminderStartedAt.IsZero() && !now.Before(item.ReminderStartedAt.Add(api.ProductStripeInvoiceSendRetryWindow))
	row.CanSendStripeReminder = item.StripeReminder && item.ReminderSentAt.IsZero() && !reminderExpired && s.canSendStripeReminder()
	switch {
	case !item.ReminderSentAt.IsZero():
		row.StripeReminderStatus = "Stripe accepted reminder at " + item.ReminderSentAt.UTC().Format("2006-01-02 15:04 UTC")
	case reminderExpired:
		row.StripeReminderStatus = "Stripe reminder needs review"
		row.StripeDashboardURL = stripeinvoice.DashboardURL(item.Livemode, url.PathEscape(item.ProviderInvoiceID))
	case !item.ReminderStartedAt.IsZero():
		row.StripeReminderStatus = "Stripe reminder pending"
	}

	if item.AmountRemainingMinor != nil {
		row.Remaining = ownerMinor(item.Currency, *item.AmountRemainingMinor)
	}
	if item.DueAt != nil {
		row.DueAt = item.DueAt.UTC().Format(time.DateOnly)
	}
	if item.StatusSyncedAt != nil {
		row.LastSync = item.StatusSyncedAt.UTC().Format("2006-01-02 15:04 UTC")
	}
	return row
}

// OwnerRefreshPayment rereads one provider invoice and renders the current
// follow-up page, including its last known state when the check fails.
func (s *Service) OwnerRefreshPayment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.ownerPaymentAction(w, r, acct, s.refreshPaymentNotice)
}

func (s *Service) OwnerCheckStripeSend(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.ownerPaymentAction(w, r, acct, s.checkStripeSendNotice)
}

type ownerPaymentAction func(context.Context, state.Account, state.ProductBillingScope, string) (string, *api.Problem)

func (s *Service) ownerPaymentAction(w http.ResponseWriter, r *http.Request, acct state.Account, run ownerPaymentAction) {
	if !customerSameOrigin(w, r) {
		return
	}
	c, ok := s.ownerContext(w, r, acct)
	if !ok {
		return
	}
	fields := []string{"csrf_token", "tenant", "period", "document", "billing_month", "payment_cursor"}
	f, err := ownerParseForm(w, r, append(fields, ownerBalanceFormFields[:]...))
	balances := ownerBalanceFromForm(f)
	if err == nil && balances != nil && (!balances.validDrilldown() || f.Has("billing_month") || f.Has("payment_cursor")) {
		err = state.ErrInvalidArgument
	}
	if err == nil {
		err = s.ownerVerify(r, c, "review", acct.ID+"/"+c.app.ID)
	}
	if err == nil && (!ownerUUID(f.Get("tenant")) || !ownerUUID(f.Get("document")) ||
		!ownerClosedBillingPeriod(f.Get("period")) || len(f.Get("payment_cursor")) > api.ProductControlOwnerCursorMaxBytes ||
		(f.Has("billing_month") && !ownerClosedBillingPeriod(f.Get("billing_month")))) {
		err = state.ErrInvalidArgument
	}
	if err != nil {
		s.ownerFormError(w, r, c, err)
		return
	}
	scope := state.ProductBillingScope{AccountID: c.account.ID, AppID: c.app.ID, TenantID: f.Get("tenant"), Period: f.Get("period")}
	notice, problem := run(r.Context(), c.account, scope, f.Get("document"))
	if balances != nil {
		s.renderOwnerBalances(w, r, c, *balances, notice, problem)
		return
	}
	s.renderOwnerPaymentAction(w, r, c, f, notice, problem)
}

func (s *Service) refreshPaymentNotice(ctx context.Context, acct state.Account, scope state.ProductBillingScope, document string) (string, *api.Problem) {
	if s.RefreshInvoicePayment == nil {
		return "", api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Payment refresh unavailable", "Payment refresh is unavailable.")
	}
	result, problem := s.RefreshInvoicePayment(ctx, acct, scope, document)
	if problem != nil {
		return "", problem
	}
	if result.PaymentStatus == state.InvoicePaymentPaid {
		return "Payment confirmed for invoice " + result.InvoiceID + ".", nil
	}
	return "Invoice " + result.InvoiceID + " has payment status " + result.PaymentStatus + ".", nil
}

func (s *Service) checkStripeSendNotice(ctx context.Context, acct state.Account, scope state.ProductBillingScope, document string) (string, *api.Problem) {
	if s.CheckStripeInvoiceSend == nil {
		return "", api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Stripe send check unavailable", "The Stripe send check is unavailable.")
	}
	result, problem := s.CheckStripeInvoiceSend(ctx, acct, scope, document)
	if problem != nil {
		return "", problem
	}
	if result.EventID == "" {
		return "Stripe previously accepted the send for invoice " + result.InvoiceID + ".", nil
	}
	return "Stripe recorded a send for invoice " + result.InvoiceID + " at " + result.EventAt.UTC().Format("2006-01-02 15:04:05 UTC") +
		" (event " + result.EventID + ").", nil
}

func (s *Service) renderOwnerPaymentAction(w http.ResponseWriter, r *http.Request, c *ownerContext, form url.Values,
	notice string, problem *api.Problem) {
	query := url.Values{}
	for _, key := range []string{"billing_month", "payment_cursor"} {
		if value := form.Get(key); value != "" {
			query.Set(key, value)
		}
	}
	if err := s.loadOwnerConsole(w, r, c, query); err != nil {
		s.ownerError(w, r, c, err)
		return
	}
	queue := c.data.PaymentQueue
	if queue == nil {
		s.ownerError(w, r, c, state.ErrNotFound)
		return
	}
	renderPaymentActionResult(w, queue, notice, problem)
	s.OwnerRender(w, r, c.account, c.data)
}

func renderPaymentActionResult(w http.ResponseWriter, queue *dashboard.ProductControlPaymentQueue, notice string, problem *api.Problem) {
	if problem == nil {
		queue.Notice = notice
		return
	}
	queue.Error = problem.Detail
	if queue.Error == "" {
		queue.Error = problem.Title
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(problem.Status)
}
