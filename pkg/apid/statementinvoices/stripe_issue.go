package statementinvoices

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// Stripe can discard API v1 idempotency keys after 24 hours. Keep a margin
// and stop retrying any attempt whose delivery might already have happened.
const stripeIssueRetryWindow = 23 * time.Hour

var stripeInvoiceID = regexp.MustCompile(`^in_[A-Za-z0-9]+$`)

func (s *Service) stripeIssueTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.StatementInvoiceExportStore, state.StatementInvoiceExport, bool) {
	store, ok := s.statementInvoiceStore(w, acct)
	if !ok {
		return nil, state.StatementInvoiceExport{}, false
	}
	statement, err := uuid.Parse(r.PathValue("statement_id"))
	if err != nil {
		httpkit.NotFound(w, "no such statement invoice")
		return nil, state.StatementInvoiceExport{}, false
	}
	export, err := store.GetStatementInvoiceExport(r.Context(), acct.ID, state.StatementSourceAppConsumer, statement.String())
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "no such statement invoice")
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not load the statement invoice"))
	case export.Provider != state.BillingProviderStripe || export.Status != state.StatementInvoiceExportCompleted ||
		export.Collection != state.CollectionNone || !stripeInvoiceID.MatchString(export.ProviderInvoiceID):
		api.WriteProblem(w, exportConflict("review and send requires a completed Stripe draft export"))
	default:
		return store, export, true
	}
	return nil, state.StatementInvoiceExport{}, false
}

func (s *Service) ReviewStripeInvoiceSend(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	store, export, ok := s.stripeIssueTarget(w, r, acct)
	if !ok {
		return
	}
	_, _, _, review, problem := s.loadStripeIssueReview(r.Context(), store, export)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, review)
}

func (s *Service) SendReviewedStripeInvoice(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	store, export, ok := s.stripeIssueTarget(w, r, acct)
	if !ok {
		return
	}
	var request api.SendStripeInvoiceRequest
	if err := httpkit.DecodeJSON(r, &request); err != nil || !validStripeSendToken(request.ReviewToken) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid invoice approval", "supply the Stripe invoice review token"))
		return
	}
	out, problem := s.issueReviewedStripeInvoice(r.Context(), store, export, request.ReviewToken)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, statementInvoiceExportResponse(out))
}

func validStripeSendToken(token string) bool {
	if len(token) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}

func (s *Service) loadStripeIssueReview(ctx context.Context, store state.StatementInvoiceExportStore,
	export state.StatementInvoiceExport) (*stripeinvoice.Client, connection, stripeinvoice.InvoiceState, api.StripeInvoiceSendReview, *api.Problem) {
	now := s.now()
	if issue := export.StripeIssue; issue != nil && issue.SentAt != nil {
		return nil, connection{}, stripeinvoice.InvoiceState{}, api.StripeInvoiceSendReview{}, exportConflict("the invoice has already been sent")
	}
	client := s.stripeInvoiceClient()
	conn, problem := s.openConnection(ctx, store, export.AccountID, state.BillingProviderStripe, stripeProvider{client: client})
	if problem != nil {
		return nil, conn, stripeinvoice.InvoiceState{}, api.StripeInvoiceSendReview{}, problem
	}
	if conn.livemode != export.Livemode {
		return nil, conn, stripeinvoice.InvoiceState{}, api.StripeInvoiceSendReview{}, draftModeConflict()
	}
	invoice, err := client.GetInvoice(ctx, conn.key, export.ProviderInvoiceID)
	if err != nil {
		return nil, conn, invoice, api.StripeInvoiceSendReview{}, issueStripeProblem(err)
	}
	if issue := export.StripeIssue; issue != nil && issue.FinalizationStarted &&
		(now.Before(issue.ApprovedAt) || !now.Before(issue.ApprovedAt.Add(stripeIssueRetryWindow))) {
		return client, conn, invoice, api.StripeInvoiceSendReview{}, exportConflict("the retry window expired; confirm delivery in Stripe")
	}
	dueAt := now.Truncate(24*time.Hour).AddDate(0, 0, export.DaysUntilDue)
	if invoice.CollectionMethod == "send_invoice" && invoice.DueDate != nil && invoice.DueDate.After(now) {
		dueAt = *invoice.DueDate
	}
	if issue := export.StripeIssue; issue != nil && issue.FinalizationStarted {
		dueAt = issue.DueAt
	}
	review, problem := buildStripeIssueReview(export, invoice, dueAt, now)
	return client, conn, invoice, review, problem
}

func buildStripeIssueReview(export state.StatementInvoiceExport, invoice stripeinvoice.InvoiceState, dueAt, now time.Time) (api.StripeInvoiceSendReview, *api.Problem) {
	if !stripeIssueInvoiceMatches(export, invoice) {
		return api.StripeInvoiceSendReview{}, exportConflict("the Stripe invoice no longer matches this statement export")
	}
	issue := export.StripeIssue
	switch {
	case invoice.AutoAdvance == nil || *invoice.AutoAdvance:
		return api.StripeInvoiceSendReview{}, exportConflict("turn off automatic advancement for this invoice in Stripe before sending")
	case invoice.Status == state.InvoicePaymentOpen:
		if issue == nil || !issue.FinalizationStarted || invoice.FinalizedAt == nil || !manualStripeTerms(invoice, dueAt) {
			return api.StripeInvoiceSendReview{}, exportConflict("this invoice has already been issued; check delivery in Stripe")
		}
	case invoice.Status != state.InvoicePaymentDraft || (issue != nil && issue.IssuedAt != nil):
		return api.StripeInvoiceSendReview{}, exportConflict("the Stripe invoice is no longer a payable draft from this review")
	}
	email, err := mail.ParseAddress(invoice.CustomerEmail)
	if err != nil || email.Address != invoice.CustomerEmail {
		return api.StripeInvoiceSendReview{}, exportConflict("set a billing email on the Stripe customer before sending")
	}
	if !dueAt.After(now) || invoice.AmountDue <= 0 || invoice.AmountPaid != 0 || invoice.AmountRemaining != invoice.AmountDue {
		return api.StripeInvoiceSendReview{}, exportConflict("sending requires a positive unpaid balance and a future due date")
	}
	unit, err := billing.StripeMinorUnit(export.Currency)
	if err != nil {
		return api.StripeInvoiceSendReview{}, statementInvoiceProblem(err)
	}
	iso, _ := api.CurrencyMinorExponent(export.Currency)
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%t\x00%d\x00%d\x00%d",
		export.ProviderInvoiceID, export.Customer, invoice.CustomerEmail, export.Currency, export.Livemode, invoice.Total, invoice.AmountDue, dueAt.Unix())))
	review := api.StripeInvoiceSendReview{InvoiceID: export.ProviderInvoiceID, StripeCustomer: export.Customer, RecipientEmail: invoice.CustomerEmail,
		Currency: export.Currency, AmountDueMinor: unit.ToISOMinor(invoice.AmountDue, iso), DueAt: dueAt.UTC(), Livemode: export.Livemode,
		ReviewToken: hex.EncodeToString(hash[:])}
	if issue != nil && issue.FinalizationStarted && issue.ReviewToken != review.ReviewToken {
		return api.StripeInvoiceSendReview{}, exportConflict("the recipient or amount changed during issuance; review the invoice in Stripe")
	}
	return review, nil
}

func stripeIssueInvoiceMatches(export state.StatementInvoiceExport, invoice stripeinvoice.InvoiceState) bool {
	return !invoice.Deleted && invoice.ID == export.ProviderInvoiceID && invoice.Customer == export.Customer &&
		invoice.Livemode != nil && *invoice.Livemode == export.Livemode && invoice.StatementID == export.StatementID &&
		invoice.SourceKind == export.SourceKind && strings.EqualFold(invoice.Currency, export.Currency)
}

func manualStripeTerms(invoice stripeinvoice.InvoiceState, dueAt time.Time) bool {
	return invoice.CollectionMethod == "send_invoice" && invoice.DueDate != nil && invoice.DueDate.Equal(dueAt)
}

func issueStripeProblem(err error) *api.Problem {
	problem := stripeCallProblem(err)
	if problem.Status == http.StatusBadGateway {
		return providerProblem(http.StatusBadGateway, "Could not confirm the Stripe operation", "retry from the same invoice to resume the reviewed send")
	}
	return problem
}

func (s *Service) issueReviewedStripeInvoice(ctx context.Context, store state.StatementInvoiceExportStore,
	export state.StatementInvoiceExport, reviewed string) (state.StatementInvoiceExport, *api.Problem) {
	issues, okIssues := s.Host.Store().(state.StripeInvoiceIssueStore)
	payments, okPayments := s.Host.Store().(state.StatementInvoicePaymentStore)
	if !okIssues || !okPayments {
		return export, api.ErrInternal("Stripe invoice delivery is unavailable")
	}
	claim := uuid.NewString()
	current, err := store.ClaimStatementInvoiceExport(ctx, export.AccountID, export.ID, claim, time.Now().Add(api.StatementInvoiceExportClaimTTL))
	if errors.Is(err, state.ErrStatementInvoiceExportInProgress) {
		return export, exportConflict("another request is issuing this invoice; try again once it finishes")
	}
	if err != nil {
		return export, api.ErrInternal("could not claim the Stripe invoice")
	}
	defer func() {
		_ = store.ReleaseStatementInvoiceExport(context.WithoutCancel(ctx), export.AccountID, export.ID, claim)
	}()
	if issue := current.StripeIssue; issue != nil && issue.SentAt != nil {
		if issue.ReviewToken != reviewed {
			return current, exportConflict("this invoice was already sent after another review")
		}
		return current, nil
	}
	client, conn, invoice, review, problem := s.loadStripeIssueReview(ctx, store, current)
	if problem != nil {
		if issue := current.StripeIssue; issue != nil && issue.IssuedAt != nil && stripeIssueInvoiceMatches(current, invoice) &&
			(invoice.Status == state.InvoicePaymentPaid || invoice.AmountPaid > 0) {
			if issue.ReviewToken != reviewed {
				return current, exportConflict("the invoice was issued after another review")
			}
			return s.recoverPaidStripeInvoiceSend(ctx, store, payments, current, claim, conn.key, invoice)
		}
		return current, problem
	}
	if review.ReviewToken != reviewed {
		return current, exportConflict("the Stripe invoice changed; open a fresh review before sending")
	}
	issue := state.StripeInvoiceIssue{ReviewToken: reviewed, DueAt: review.DueAt, ApprovedAt: s.now()}
	if prior := current.StripeIssue; prior != nil && prior.FinalizationStarted {
		issue = *prior
	}
	save := func() *api.Problem {
		if err := issues.SaveStripeInvoiceIssue(ctx, current.AccountID, current.ID, claim, issue); err != nil {
			return api.ErrInternal("could not save the Stripe invoice delivery progress")
		}
		current.StripeIssue = &issue
		return nil
	}
	if invoice.Status == state.InvoicePaymentDraft && !manualStripeTerms(invoice, review.DueAt) {
		err = client.SetManualInvoiceTerms(ctx, conn.key, stripeIdempotencyKey(&current, "manual-terms-"+reviewed),
			current.ProviderInvoiceID, review.DueAt)
		if err != nil {
			return current, issueStripeProblem(err)
		}
		invoice, err = client.GetInvoice(ctx, conn.key, current.ProviderInvoiceID)
		if err != nil {
			return current, issueStripeProblem(err)
		}
		updated, problem := buildStripeIssueReview(current, invoice, review.DueAt, s.now())
		if problem != nil {
			return current, problem
		}
		if invoice.Status != state.InvoicePaymentDraft || !manualStripeTerms(invoice, review.DueAt) || updated.ReviewToken != reviewed {
			return current, exportConflict("Stripe changed the draft terms; open a fresh review")
		}
	}
	if issue.IssuedAt == nil {
		if !issue.FinalizationStarted {
			issue.FinalizationStarted = true
			if problem := save(); problem != nil {
				return current, problem
			}
		}
		// When a response was lost, replaying the finalize key confirms that
		// this workflow finalized the invoice. An open invoice alone cannot.
		invoice, err = client.FinalizeManualInvoice(ctx, conn.key, stripeIdempotencyKey(&current, "manual-finalize"), current.ProviderInvoiceID)
		if err != nil {
			return current, issueStripeProblem(err)
		}
		if invoice.Status != state.InvoicePaymentOpen {
			return current, exportConflict("Stripe did not issue an open, payable invoice; review it in Stripe")
		}
		if _, problem := buildStripeIssueReview(current, invoice, review.DueAt, s.now()); problem != nil {
			return current, problem
		}
		issue.IssuedAt = invoice.FinalizedAt
		if problem := save(); problem != nil {
			return current, problem
		}
	}
	// A replayed finalization response can be older than the current Stripe
	// state. Check the live invoice again before any customer communication.
	invoice, err = client.GetInvoice(ctx, conn.key, current.ProviderInvoiceID)
	if err != nil {
		return current, issueStripeProblem(err)
	}
	if _, problem := buildStripeIssueReview(current, invoice, review.DueAt, s.now()); problem != nil {
		return current, problem
	}
	sent, err := client.SendInvoice(ctx, conn.key, stripeIdempotencyKey(&current, "manual-send"), current.ProviderInvoiceID)
	if err != nil {
		return current, issueStripeProblem(err)
	}
	if sent.ID != current.ProviderInvoiceID {
		return current, api.ErrInternal("Stripe returned a different invoice after sending")
	}
	now := s.now()
	issue.SentAt = &now
	if problem := save(); problem != nil {
		return current, problem
	}
	if payment, known, _ := stripeInvoicePayment(sent); known {
		// Delivery is already checkpointed, so the existing worker can
		// refresh payment state if this write fails.
		_, _ = payments.RecordStatementInvoicePayment(ctx, current.ID, payment, now, nextInvoiceSync(payment, now))
	}
	s.Host.Audit(ctx, "statement_invoice.stripe_sent", current.AccountID, map[string]any{
		"statement_id": current.StatementID, "invoice_id": current.ProviderInvoiceID, "livemode": current.Livemode,
		"currency": review.Currency, "amount_due_minor": review.AmountDueMinor, "due_at": review.DueAt,
		"exported_at": current.CompletedAt, "issued_at": issue.IssuedAt, "sent_at": now,
	})
	out, err := store.GetStatementInvoiceExport(ctx, current.AccountID, current.SourceKind, current.StatementID)
	if err != nil {
		return current, api.ErrInternal("could not reload the sent Stripe invoice")
	}
	return out, nil
}

// reconcileStripeInvoiceSend records Stripe's receipt under the export
// claim. A payment can arrive via a hosted link without any email.
func (s *Service) reconcileStripeInvoiceSend(ctx context.Context, export state.StatementInvoiceExport, claim, key string) (state.StatementInvoiceExport, error) {
	issue := pendingStripeInvoiceDelivery(export)
	if issue == nil {
		return export, nil
	}
	issues, ok := s.Host.Store().(state.StripeInvoiceIssueStore)
	if !ok {
		return export, errors.New("stripe invoice delivery is unavailable")
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	sentAt, err := s.stripeInvoiceClient().FindInvoiceSentAt(lookupCtx, key, export.ProviderInvoiceID,
		stripeIdempotencyKey(&export, "manual-send"), export.Livemode, *issue.IssuedAt, issue.ApprovedAt.Add(stripeIssueRetryWindow))
	if err != nil || sentAt == nil {
		return export, err
	}
	checkpoint := *issue
	checkpoint.SentAt = sentAt
	if err := issues.SaveStripeInvoiceIssue(ctx, export.AccountID, export.ID, claim, checkpoint); err != nil {
		return export, err
	}
	export.StripeIssue = &checkpoint
	s.Host.Audit(ctx, "statement_invoice.stripe_sent", export.AccountID, map[string]any{
		"statement_id": export.StatementID, "invoice_id": export.ProviderInvoiceID, "livemode": export.Livemode,
		"currency": export.Currency, "due_at": issue.DueAt, "exported_at": export.CompletedAt,
		"issued_at": issue.IssuedAt, "sent_at": sentAt, "reconciled": true,
	})
	return export, nil
}

func pendingStripeInvoiceDelivery(export state.StatementInvoiceExport) *state.StripeInvoiceIssue {
	issue := export.StripeIssue
	if export.Provider == state.BillingProviderStripe && issue != nil && issue.IssuedAt != nil && issue.SentAt == nil {
		return issue
	}
	return nil
}

func (s *Service) recoverPaidStripeInvoiceSend(ctx context.Context, store state.StatementInvoiceExportStore, payments state.StatementInvoicePaymentStore,
	current state.StatementInvoiceExport, claim, key string, invoice stripeinvoice.InvoiceState) (state.StatementInvoiceExport, *api.Problem) {
	current, err := s.reconcileStripeInvoiceSend(ctx, current, claim, key)
	switch {
	case errors.Is(err, stripeinvoice.ErrKeyRejected):
		return current, providerProblem(http.StatusUnprocessableEntity, "Stripe delivery confirmation needs Events read access",
			"grant the connected Stripe key Events read access, then check the invoice again")
	case err != nil:
		return current, providerProblem(http.StatusBadGateway, "Could not confirm Stripe invoice delivery", "try checking the invoice again")
	case pendingStripeInvoiceDelivery(current) != nil:
		return current, exportConflict("Stripe shows a payment but has no matching send receipt; check delivery in Stripe")
	}
	if payment, known, _ := stripeInvoicePayment(invoice); known {
		_ = s.recordInvoicePayment(ctx, payments, current, payment, s.now())
	}
	out, err := store.GetStatementInvoiceExport(ctx, current.AccountID, current.SourceKind, current.StatementID)
	if err != nil {
		return current, api.ErrInternal("could not reload the sent Stripe invoice")
	}
	return out, nil
}
