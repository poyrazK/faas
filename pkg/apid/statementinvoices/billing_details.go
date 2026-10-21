package statementinvoices

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/paddleinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// Customer billing details live only in the owner's Paddle
// (ADR-20261020120000): these read and write the Paddle customer's active
// address and business with the account's sealed Paddle key.

func billingDetailsUnavailable(detail string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeProductBillingDetailsUnavailable, "Billing details unavailable", detail)
}

// paddleCustomer opens the account's Paddle connection for a Paddle
// customer ID.
func (s *Service) paddleCustomer(ctx context.Context, acct state.Account, customerID string) (connection, paddleProvider, *api.Problem) {
	p := paddleProvider{client: s.paddleClient()}
	if !paddleCustomerIDRE.MatchString(customerID) {
		return connection{}, p, billingDetailsUnavailable("your product team does not invoice this workspace through Paddle")
	}
	store, ok := s.Host.Store().(state.StatementInvoiceExportStore)
	if !ok {
		return connection{}, p, api.ErrInternal("billing connections are unavailable")
	}
	if _, err := store.GetBillingConnection(ctx, acct.ID, state.BillingProviderPaddle); errors.Is(err, state.ErrNotFound) {
		return connection{}, p, billingDetailsUnavailable("your product team has not connected Paddle")
	}
	conn, problem := s.openConnection(ctx, store, acct.ID, state.BillingProviderPaddle, p)
	return conn, p, problem
}

func billingDetailsProblem(err error) *api.Problem {
	if errors.Is(err, paddleinvoice.ErrKeyRejected) {
		return billingDetailsUnavailable("your product team's Paddle key cannot manage billing addresses")
	}
	return paddleCallProblem(err)
}

// PaddleBillingDetails reads a Paddle customer's active address and
// business.
func (s *Service) PaddleBillingDetails(ctx context.Context, acct state.Account, customerID string) (api.ProductBillingDetails, *api.Problem) {
	conn, p, problem := s.paddleCustomer(ctx, acct, customerID)
	if problem != nil {
		return api.ProductBillingDetails{}, problem
	}
	out := api.ProductBillingDetails{Provider: state.BillingProviderPaddle}
	address, ok, err := p.client.ActiveAddress(ctx, conn.key, customerID)
	if err != nil {
		return out, billingDetailsProblem(err)
	}
	if ok {
		out.Address = &api.ProductBillingAddress{CountryCode: address.CountryCode, PostalCode: address.PostalCode, Region: address.Region,
			City: address.City, FirstLine: address.FirstLine, SecondLine: address.SecondLine}
	}
	business, ok, err := p.client.ActiveBusiness(ctx, conn.key, customerID)
	if err != nil {
		return out, billingDetailsProblem(err)
	}
	if ok {
		out.Business = &api.ProductBillingBusiness{Name: business.Name, TaxIdentifier: business.TaxIdentifier}
	}
	return out, nil
}

// PutPaddleBillingDetails updates the Paddle customer's active address, or
// creates one, and its business when the request has one. It returns the
// parts it changed.
func (s *Service) PutPaddleBillingDetails(ctx context.Context, acct state.Account, customerID string,
	req api.PutProductBillingDetailsRequest) (api.ProductBillingDetails, []string, *api.Problem) {
	if err := req.Validate(); err != nil {
		return api.ProductBillingDetails{}, nil, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid billing details", err.Error())
	}
	conn, p, problem := s.paddleCustomer(ctx, acct, customerID)
	if problem != nil {
		return api.ProductBillingDetails{}, nil, problem
	}
	current, ok, err := p.client.ActiveAddress(ctx, conn.key, customerID)
	if err != nil {
		return api.ProductBillingDetails{}, nil, billingDetailsProblem(err)
	}
	a := req.Address
	want := paddleinvoice.Address{CountryCode: a.CountryCode, PostalCode: a.PostalCode, Region: a.Region, City: a.City, FirstLine: a.FirstLine, SecondLine: a.SecondLine}
	changed := []string{}
	if ok {
		want.ID = current.ID
	}
	if !ok || addressDiffers(current, want) {
		if _, err := p.client.PutAddress(ctx, conn.key, customerID, want); err != nil {
			return api.ProductBillingDetails{}, nil, billingDetailsProblem(err)
		}
		changed = append(changed, "address")
	}
	if req.Business != nil {
		existing, ok, err := p.client.ActiveBusiness(ctx, conn.key, customerID)
		if err != nil {
			return api.ProductBillingDetails{}, changed, billingDetailsProblem(err)
		}
		next := paddleinvoice.Business{Name: req.Business.Name, TaxIdentifier: req.Business.TaxIdentifier}
		if ok {
			next.ID = existing.ID
		}
		if !ok || existing.Name != next.Name || existing.TaxIdentifier != next.TaxIdentifier {
			if _, err := p.client.PutBusiness(ctx, conn.key, customerID, next); err != nil {
				return api.ProductBillingDetails{}, changed, billingDetailsProblem(err)
			}
			changed = append(changed, "business")
		}
	}
	out, problem := s.PaddleBillingDetails(ctx, acct, customerID)
	return out, changed, problem
}

func addressDiffers(current, want paddleinvoice.Address) bool {
	return current.CountryCode != want.CountryCode || current.PostalCode != want.PostalCode || current.Region != want.Region ||
		current.City != want.City || current.FirstLine != want.FirstLine || current.SecondLine != want.SecondLine
}
