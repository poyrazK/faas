package statementinvoices

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/paddleinvoice"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// Self-serve signup creates the customer in the owner's own provider, and a
// Stripe customer can keep a card on file there through Stripe's hosted
// Checkout (ADR-20261022100735). Gregale never sees card data.

func signupUnavailable(detail string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeProductSignupUnavailable, "Signup unavailable", detail)
}

func paymentMethodUnavailable(detail string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeProductPaymentMethodUnavailable, "Payment method unavailable", detail)
}

func (s *Service) connectedProvider(ctx context.Context, acct state.Account, name string, p provider) (connection, *api.Problem) {
	store, ok := s.Host.Store().(state.StatementInvoiceExportStore)
	if !ok {
		return connection{}, api.ErrInternal("billing connections are unavailable")
	}
	if _, err := store.GetBillingConnection(ctx, acct.ID, name); errors.Is(err, state.ErrNotFound) {
		return connection{}, signupUnavailable("the product team has not connected " + p.title())
	}
	return s.openConnection(ctx, store, acct.ID, name, p)
}

// SignupCustomer creates the signup's customer in the account's Stripe, or
// finds or creates it in its Paddle, and returns its provider ID.
func (s *Service) SignupCustomer(ctx context.Context, acct state.Account, provider, idempotencyKey, email, name string, metadata map[string]string) (string, *api.Problem) {
	switch provider {
	case state.BillingProviderStripe:
		p := stripeProvider{client: s.stripeInvoiceClient()}
		conn, problem := s.connectedProvider(ctx, acct, provider, p)
		if problem != nil {
			return "", problem
		}
		id, err := p.client.CreateCustomer(ctx, conn.key, idempotencyKey, email, name, metadata)
		if errors.Is(err, stripeinvoice.ErrKeyRejected) {
			return "", signupUnavailable("the product team's Stripe key cannot create customers")
		}
		if err != nil {
			return "", stripeCallProblem(err)
		}
		if !stripeCustomerIDRE.MatchString(id) {
			return "", api.ErrInternal("Stripe returned no customer ID")
		}
		return id, nil
	case state.BillingProviderPaddle:
		p := paddleProvider{client: s.paddleClient()}
		conn, problem := s.connectedProvider(ctx, acct, provider, p)
		if problem != nil {
			return "", problem
		}
		id, err := p.client.CustomerByEmail(ctx, conn.key, email, name)
		if errors.Is(err, paddleinvoice.ErrKeyRejected) {
			return "", signupUnavailable("the product team's Paddle key cannot manage customers")
		}
		if err != nil {
			return "", paddleCallProblem(err)
		}
		if !paddleCustomerIDRE.MatchString(id) {
			return "", api.ErrInternal("Paddle returned no customer ID")
		}
		return id, nil
	}
	return "", signupUnavailable("signup creates customers in Stripe or Paddle only")
}

func (s *Service) stripeCustomer(ctx context.Context, acct state.Account, customer string) (connection, stripeProvider, *api.Problem) {
	p := stripeProvider{client: s.stripeInvoiceClient()}
	if !stripeCustomerIDRE.MatchString(customer) {
		return connection{}, p, paymentMethodUnavailable("your product team does not bill this workspace through Stripe")
	}
	conn, problem := s.connectedProvider(ctx, acct, state.BillingProviderStripe, p)
	if problem != nil && problem.Code == api.CodeProductSignupUnavailable {
		return connection{}, p, paymentMethodUnavailable("your product team has not connected Stripe")
	}
	return conn, p, problem
}

func paymentMethodProblem(err error) *api.Problem {
	if errors.Is(err, stripeinvoice.ErrKeyRejected) {
		return paymentMethodUnavailable("your product team's Stripe key cannot manage payment methods")
	}
	return stripeCallProblem(err)
}

// StripePaymentMethod returns a Stripe customer's default invoice card.
func (s *Service) StripePaymentMethod(ctx context.Context, acct state.Account, customer string) (*api.ProductPaymentMethod, *api.Problem) {
	conn, p, problem := s.stripeCustomer(ctx, acct, customer)
	if problem != nil {
		return nil, problem
	}
	card, ok, err := p.client.DefaultPaymentMethod(ctx, conn.key, customer)
	if err != nil {
		return nil, paymentMethodProblem(err)
	}
	if !ok {
		return nil, nil
	}
	return &api.ProductPaymentMethod{Provider: state.BillingProviderStripe, Brand: card.Brand, Last4: card.Last4, ExpMonth: card.ExpMonth, ExpYear: card.ExpYear}, nil
}

// StripeSetupCheckout opens a Stripe-hosted Checkout Session that saves a
// card to the customer, returning its URL.
func (s *Service) StripeSetupCheckout(ctx context.Context, acct state.Account, customer, currency, idempotencyKey, successURL, cancelURL string) (string, *api.Problem) {
	conn, p, problem := s.stripeCustomer(ctx, acct, customer)
	if problem != nil {
		return "", problem
	}
	session, err := p.client.CreateSetupCheckout(ctx, conn.key, idempotencyKey, customer, currency, successURL, cancelURL)
	if err != nil {
		return "", paymentMethodProblem(err)
	}
	if api.ProviderHostedURL(session.URL) == "" {
		return "", api.ErrInternal("Stripe returned no https Checkout page")
	}
	return session.URL, nil
}

// CompleteStripeSetup makes the card a completed setup Checkout Session saved
// for this customer the customer's default for invoices.
func (s *Service) CompleteStripeSetup(ctx context.Context, acct state.Account, customer, sessionID string) (*api.ProductPaymentMethod, *api.Problem) {
	conn, p, problem := s.stripeCustomer(ctx, acct, customer)
	if problem != nil {
		return nil, problem
	}
	session, err := p.client.GetCheckoutSession(ctx, conn.key, sessionID)
	if err != nil {
		return nil, paymentMethodProblem(err)
	}
	if session.Mode != "setup" || session.Customer != customer || session.Status != "complete" || session.SetupIntent == nil ||
		session.SetupIntent.Status != "succeeded" || session.SetupIntent.PaymentMethod == "" {
		return nil, paymentMethodUnavailable("this Checkout did not save a card for your workspace; start again")
	}
	if err := p.client.SetDefaultPaymentMethod(ctx, conn.key, customer, session.SetupIntent.PaymentMethod); err != nil {
		return nil, paymentMethodProblem(err)
	}
	return s.StripePaymentMethod(ctx, acct, customer)
}
