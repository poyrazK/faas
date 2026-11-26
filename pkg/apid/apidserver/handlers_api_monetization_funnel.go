package apidserver

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	apiMonetizationCohortDays  = 120
	apiMonetizationCohortLimit = 50
)

// APIMonetizationFunnelResponse reports observed milestones for a registration cohort.
type APIMonetizationFunnelResponse struct {
	Since                time.Time                `json:"since"`
	Until                time.Time                `json:"until"`
	TotalRegistered      int                      `json:"total_registered"`
	Registered           int                      `json:"registered"`
	PricedInFirst30Days  int                      `json:"priced_in_first_30_days"`
	LiveInvoiceSent      int                      `json:"live_invoice_sent"`
	LivePayment          int                      `json:"live_payment"`
	NextBillableMonth    int                      `json:"next_billable_month"`
	NextMonthLivePayment int                      `json:"next_month_live_payment"`
	SecondInvoiceReview  []APISecondInvoiceReview `json:"second_invoice_review"`
}

// APISecondInvoiceReview identifies the next statement to work on for one
// customer who has already made a live payment.
type APISecondInvoiceReview struct {
	ConsumerID  string `json:"consumer_id"`
	Name        string `json:"name"`
	Month       string `json:"month"`
	StatementID string `json:"statement_id"`
	Status      string `json:"status"`
	HoldRunID   string `json:"hold_run_id,omitempty"`
}

func (s *server) getAPIMonetizationFunnel(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.consumerFeatureAllowed(w, acct) {
		return
	}
	app, ok := s.consumerApp(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.APIMonetizationFunnelStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("API monetization funnel is unavailable"))
		return
	}
	ctx := r.Context()
	out, err := s.apiMonetizationFunnel(ctx, store, acct.ID, app.ID, time.Now().UTC())
	if err != nil {
		s.log.ErrorContext(ctx, "load API monetization funnel", "app_id", app.ID, "err", err)
		api.WriteProblem(w, api.ErrInternal("could not load API monetization funnel"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiMonetizationFunnel(ctx context.Context, store state.APIMonetizationFunnelStore,
	accountID, appID string, now time.Time) (APIMonetizationFunnelResponse, error) {
	out := APIMonetizationFunnelResponse{
		Since: now.AddDate(0, 0, -apiMonetizationCohortDays), Until: now,
		SecondInvoiceReview: make([]APISecondInvoiceReview, 0),
	}
	cohort, err := store.ListAPIConsumerMonetizationCohort(ctx, accountID, appID, out.Since, out.Until, apiMonetizationCohortLimit)
	if err != nil {
		return out, err
	}
	out.TotalRegistered, out.Registered = cohort.Total, len(cohort.Customers)
	for _, c := range cohort.Customers {
		start := c.RegisteredAt.UTC().Truncate(time.Minute)
		end := start.AddDate(0, 0, consumerBillingProgressDays)
		if lastMinute := now.Truncate(time.Minute); lastMinute.Before(end) {
			end = lastMinute
		}
		_, firstPriced, err := s.consumerBillingProgressUsage(ctx, accountID, appID, c.ID, start, end)
		if err != nil {
			return out, err
		}
		if firstPriced != nil {
			out.PricedInFirst30Days++
		}
		out.addInvoiceMilestones(c, now)
	}
	return out, nil
}

func (out *APIMonetizationFunnelResponse) addInvoiceMilestones(c state.APIConsumerMonetizationCustomer, now time.Time) {
	var firstPaid *state.APIConsumerMonetizationPeriod
	sent := false
	for i := range c.Periods {
		p := &c.Periods[i]
		if p.Draft {
			continue
		}
		sent = sent || milestoneObserved(p.SentAt, c.RegisteredAt, now)
		if milestoneObserved(p.PaidAt, c.RegisteredAt, now) && (firstPaid == nil ||
			p.PaidAt.Before(*firstPaid.PaidAt) || p.PaidAt.Equal(*firstPaid.PaidAt) &&
			(p.Start.Before(firstPaid.Start) || p.Start.Equal(firstPaid.Start) && p.End.Before(firstPaid.End))) {
			firstPaid = p
		}
	}
	if sent {
		out.LiveInvoiceSent++
	}
	if firstPaid == nil {
		return
	}
	out.LivePayment++

	// Drafts can need attention before a month qualifies for the funnel.
	// A finalized billable month always determines the repeat milestone.
	firstMonth := billing.MonthStart(firstPaid.Start)
	var next, draft *state.APIConsumerMonetizationPeriod
	for i := range c.Periods {
		p := &c.Periods[i]
		if p.Start.Before(firstPaid.End) || !billing.MonthStart(p.Start).After(firstMonth) {
			continue
		}
		if p.Draft {
			draft = preferSecondInvoicePeriod(draft, p, *firstPaid.PaidAt, now)
		} else {
			next = preferSecondInvoicePeriod(next, p, *firstPaid.PaidAt, now)
		}
	}
	if next != nil {
		out.NextBillableMonth++
		if milestoneObserved(next.PaidAt, *firstPaid.PaidAt, now) {
			out.NextMonthLivePayment++
		}
	} else {
		next = draft
	}
	if next != nil {
		status, _ := secondInvoiceStatus(next, *firstPaid.PaidAt, now)
		out.SecondInvoiceReview = append(out.SecondInvoiceReview, APISecondInvoiceReview{
			ConsumerID: c.ID, Name: c.Name, Month: next.Start.UTC().Format("2006-01"),
			StatementID: next.StatementID, Status: status, HoldRunID: next.UsageHoldRun,
		})
	}
}

func preferSecondInvoicePeriod(current, candidate *state.APIConsumerMonetizationPeriod, firstPaidAt, now time.Time) *state.APIConsumerMonetizationPeriod {
	if current == nil {
		return candidate
	}
	month, currentMonth := billing.MonthStart(candidate.Start), billing.MonthStart(current.Start)
	if month.Before(currentMonth) {
		return candidate
	}
	if month.Equal(currentMonth) {
		_, rank := secondInvoiceStatus(candidate, firstPaidAt, now)
		_, currentRank := secondInvoiceStatus(current, firstPaidAt, now)
		if rank > currentRank || rank == currentRank && candidate.StatementID < current.StatementID {
			return candidate
		}
	}
	return current
}

func secondInvoiceStatus(p *state.APIConsumerMonetizationPeriod, firstPaidAt, now time.Time) (label string, rank int) {
	switch {
	case milestoneObserved(p.PaidAt, firstPaidAt, now):
		return "paid", 8
	case p.UsageHoldRun != "":
		return "usage_hold", 7
	case p.InvoiceProvider == state.BillingProviderStripe && p.InvoiceLive && p.PaymentStatus == state.InvoicePaymentOpen:
		return "awaiting_payment", 6
	case p.InvoiceProvider == state.BillingProviderStripe && p.InvoiceLive && p.PaymentStatus == state.InvoicePaymentDraft:
		return "stripe_draft", 5
	case p.InvoiceProvider == state.BillingProviderStripe && !p.InvoiceLive:
		return "test_invoice", 4
	case p.InvoiceProvider != "":
		return "review_invoice", 3
	case p.Draft:
		return "statement_draft", 2
	default:
		return "ready_to_export", 1
	}
}

func milestoneObserved(at *time.Time, since, until time.Time) bool {
	return at != nil && !at.Before(since) && !at.After(until)
}
