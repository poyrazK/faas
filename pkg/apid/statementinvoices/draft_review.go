package statementinvoices

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// ReviewAppConsumerDraft checks local Stripe export prerequisites. The usage
// statement may still be a draft with a projected credit application.
func (s *Service) ReviewAppConsumerDraft(ctx context.Context, acct state.Account, consumer state.APIConsumer,
	statement state.APIConsumerUsageStatement, conn state.BillingConnection) (string, *api.StatementInvoiceExportResponse, *api.Problem) {
	store, ok := s.Host.Store().(state.StatementInvoiceExportStore)
	if !ok {
		return "", nil, api.ErrInternal("statement invoice exports are unavailable")
	}
	prior, err := store.GetStatementInvoiceExport(ctx, acct.ID, state.StatementSourceAppConsumer, statement.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return "", nil, api.ErrInternal("could not load the statement invoice export")
	}
	if prior.Status == state.StatementInvoiceExportCompleted {
		if prior.Provider != state.BillingProviderStripe || prior.Collection != "none" {
			return "", nil, exportConflict("this statement already has an invoice")
		}
		export := statementInvoiceExportResponse(prior)
		return prior.Customer, &export, nil
	}
	if conn.Provider == "" {
		return "", nil, exportConflict("connect Stripe to export drafts")
	}
	if conn.Collection != "none" {
		return "", nil, draftCollectionConflict()
	}
	if _, ok := s.openKey(acct.ID, conn); !ok {
		return "", nil, exportConflict("the Stripe key could not be opened; reconnect Stripe")
	}
	linked, problem := s.linkedStripeCustomer(ctx, acct.ID, consumer.ID, conn.StripeAccountID, conn.Livemode)
	if problem != nil {
		return "", nil, problem
	}
	customer, problem := (stripeProvider{}).customer(linked, consumer.ExternalRef)
	if problem != nil {
		return "", nil, api.NewProblem(http.StatusConflict, api.CodeStripeCustomerMissing, "Stripe customer required",
			"link this app customer to a Stripe customer before exporting")
	}
	src, problem := s.appConsumerInvoiceSource(ctx, acct, consumer, statement, customer)
	if problem != nil {
		return "", nil, problem
	}
	unit, err := billing.StripeMinorUnit(src.invoice.Currency)
	if err != nil {
		return "", nil, statementInvoiceProblem(err)
	}
	if unit.FromMillicents(src.invoice.TotalMillicents) <= 0 {
		return "", nil, providerProblem(http.StatusUnprocessableEntity, "Amount below Stripe precision", "the amount due rounds to zero in the invoice currency")
	}
	if prior.Provider != "" {
		if prior.Provider != state.BillingProviderStripe || prior.Customer != customer ||
			prior.Collection != "none" || prior.Livemode != conn.Livemode {
			return "", nil, exportConflict("an earlier export used a different provider, customer or collection mode")
		}
		if conn.UpdatedAt.After(prior.CreatedAt) {
			return "", nil, exportConflict("the Stripe connection changed after this export started")
		}
	}
	if src.existingHandoff != "" {
		if src.existingHandoff != prior.ProviderInvoiceID {
			return "", nil, exportConflict("the statement was already handed off to another invoice")
		}
	} else if problem := s.checkHandoffAllowed(ctx, acct, src); problem != nil {
		return "", nil, problem
	}
	return customer, nil, nil
}

func (s *Service) linkedStripeCustomer(ctx context.Context, accountID, consumerID, stripeAccountID string, livemode bool) (string, *api.Problem) {
	if stripeAccountID == "" {
		return "", nil
	}
	links, ok := s.Host.Store().(state.StripeConsumerLinkStore)
	if !ok {
		return "", api.ErrInternal("Stripe customer links are unavailable")
	}
	customer, err := links.GetStripeConsumerCustomer(ctx, accountID, consumerID, stripeAccountID, livemode)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return "", api.ErrInternal("could not read the Stripe customer link")
	}
	return customer, nil
}

// LinkAppConsumerStripeCustomer verifies and saves the customer without
// invoicing or changing the app consumer's external reference.
func (s *Service) LinkAppConsumerStripeCustomer(ctx context.Context, acct state.Account, consumer state.APIConsumer,
	conn state.BillingConnection, customer string) *api.Problem {
	if !stripeCustomerIDRE.MatchString(customer) || len(customer) > 255 {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid Stripe customer", "enter a Stripe customer ID beginning with cus_")
	}
	store, ok := s.Host.Store().(state.StripeConsumerLinkStore)
	if !ok {
		return api.ErrInternal("Stripe customer links are unavailable")
	}
	key, ok := s.openKey(acct.ID, conn)
	if !ok {
		return exportConflict("the Stripe key could not be opened; reconnect Stripe")
	}
	client := s.stripeInvoiceClient()
	stripeAccount, err := client.AccountID(ctx, key)
	if err != nil {
		return stripeLinkProblem(err)
	}
	if conn.StripeAccountID != "" && conn.StripeAccountID != stripeAccount {
		return exportConflict("the Stripe account changed; reconnect Stripe and review the draft again")
	}
	valid, err := client.VerifyCustomer(ctx, key, customer, conn.Livemode)
	var stripeErr *stripeinvoice.Error
	if errors.As(err, &stripeErr) && (stripeErr.Status == http.StatusNotFound || stripeErr.Code == "resource_missing") {
		return stripeCustomerUnavailable()
	}
	if err != nil {
		return stripeLinkProblem(err)
	}
	if !valid {
		return stripeCustomerUnavailable()
	}
	changed, err := store.LinkStripeConsumerCustomer(ctx, state.StripeConsumerLink{
		AccountID: acct.ID, ConsumerID: consumer.ID, StripeAccountID: stripeAccount,
		Livemode: conn.Livemode, StripeCustomerID: customer,
	}, conn.UpdatedAt)
	if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
		return exportConflict("the Stripe connection or customer link changed; review the draft again")
	}
	if err != nil {
		return api.ErrInternal("could not save the Stripe customer link")
	}
	if changed {
		s.Host.Audit(ctx, "api_consumer.stripe_customer_linked", acct.ID, map[string]any{
			"app_id": consumer.AppID, "consumer_id": consumer.ID, "stripe_account_id": stripeAccount,
			"stripe_customer_id": customer, "livemode": conn.Livemode,
		})
	}
	return nil
}

func stripeCustomerUnavailable() *api.Problem {
	return providerProblem(http.StatusUnprocessableEntity, "Stripe customer unavailable",
		"choose an active customer from the connected Stripe account and mode")
}

func stripeLinkProblem(err error) *api.Problem {
	if errors.Is(err, stripeinvoice.ErrKeyRejected) {
		return providerProblem(http.StatusUnprocessableEntity, "Stripe customer verification needs read access",
			"grant the connected key read access to the Stripe account and customers")
	}
	return providerProblem(http.StatusBadGateway, "Could not verify the Stripe customer", "try again")
}

// ExportReviewedAppConsumerDraft uses the existing provider claim and
// invoice receipt, bound to the reviewed Stripe connection and customer.
func (s *Service) ExportReviewedAppConsumerDraft(ctx context.Context, acct state.Account, consumer state.APIConsumer,
	statement state.APIConsumerUsageStatement, customer string, conn state.BillingConnection) (api.StatementInvoiceExportResponse, *api.Problem) {
	if statement.Status != state.APIConsumerUsageStatementFinalized {
		return api.StatementInvoiceExportResponse{}, notFinalizedProblem()
	}
	store, ok := s.Host.Store().(state.StatementInvoiceExportStore)
	if !ok {
		return api.StatementInvoiceExportResponse{}, api.ErrInternal("statement invoice exports are unavailable")
	}
	src, problem := s.appConsumerInvoiceSource(ctx, acct, consumer, statement, customer)
	if problem != nil {
		return api.StatementInvoiceExportResponse{}, problem
	}
	src.draftLivemode, src.draftConnectionAt = &conn.Livemode, conn.UpdatedAt
	export, problem := s.exportAppConsumer(ctx, acct, store, src, state.BillingProviderStripe)
	if problem != nil {
		return api.StatementInvoiceExportResponse{}, problem
	}
	return statementInvoiceExportResponse(export), nil
}
