package product

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

type ownerReceiptFixture struct {
	state.ProductControlStore
	evidence state.ProductReceiptSnapshot
}

func (f ownerReceiptFixture) GetProductReceipt(context.Context, string, string, string) (state.ProductReceiptSnapshot, error) {
	return f.evidence, nil
}

type ownerBillingFixture struct {
	state.ProductBillingStore
	review api.ProductBillingReview
}

func (f ownerBillingFixture) ReviewProductBilling(context.Context, state.ProductBillingScope) (api.ProductBillingReview, error) {
	return f.review, nil
}

func TestProductControlOwnerCreditReview(t *testing.T) {
	store := state.NewMemStore()
	tenant, operation := uuid.NewString(), uuid.NewString()
	evidence := state.ProductReceiptSnapshot{ProductReceiptEvidence: api.ProductReceiptEvidence{OperationID: operation, TenantID: tenant, ResolvedState: api.OperationSucceeded, Receipt: api.ProductOperationReceipt{Period: "2026-09", Currency: "EUR", UnitPriceMillicents: 25000}}}
	products := ownerReceiptFixture{ProductControlStore: store, evidence: evidence}
	billing := ownerBillingFixture{ProductBillingStore: store, review: api.ProductBillingReview{Finalized: &api.ProductBillingDocument{ID: uuid.NewString()}}}
	c := &ownerContext{products: products, billing: billing, data: dashboard.ProductControlData{Confirmation: &dashboard.ProductControlConfirmation{}}}
	a := ownerAction{Action: "credit", Tenant: tenant, Period: "2026-09", Operation: operation, Amount: "1234", Reason: "Export service credit"}
	s := &Service{}
	if err := s.reviewOwnerReceipt(httptest.NewRequest("POST", "/", nil), c, &a); err != nil {
		t.Fatal(err)
	}
	if !ownerUUID(a.ID) || ownerValidateAction(a, true) != nil {
		t.Fatalf("missing stable credit identity: %+v", a)
	}
	var details strings.Builder
	for _, d := range c.data.Confirmation.Details {
		details.WriteString(d.After)
	}
	for _, want := range []string{"EUR -0.01234", operation, billing.review.Finalized.ID, a.ID} {
		if !strings.Contains(details.String(), want) {
			t.Fatalf("credit confirmation omitted %q", want)
		}
	}
	if decoded, err := ownerDecodePayload(ownerPayload(a)); err != nil || decoded != a {
		t.Fatalf("confirmation lost original request: %+v %v", decoded, err)
	}
	for _, name := range []string{"wrong customer", "wrong period", "unverified outcome", "unfinalized", "too much"} {
		t.Run(name, func(t *testing.T) {
			p, b, request := products, billing, a
			switch name {
			case "wrong customer":
				request.Tenant = uuid.NewString()
			case "wrong period":
				request.Period = "2026-08"
			case "unverified outcome":
				p.evidence.ResolvedState = ""
			case "unfinalized":
				b.review.Finalized = nil
			case "too much":
				request.Amount = "25001"
			}
			view := &ownerContext{products: p, billing: b, data: dashboard.ProductControlData{Confirmation: &dashboard.ProductControlConfirmation{}}}
			if err := s.reviewOwnerReceipt(httptest.NewRequest("POST", "/", nil), view, &request); !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrConflict) {
				t.Fatalf("invalid credit review: %v", err)
			}
		})
	}
}

func TestProductControlOwnerStrictFormsAndMoney(t *testing.T) {
	base := url.Values{"action": {"credit"}, "tenant": {uuid.NewString()}, "period": {"2026-09"}, "operation": {uuid.NewString()}, "amount": {"1234"}, "reason": {"Export credit"}}
	for _, extra := range []string{"&amount=2", "&customer=ignored", "&amount=9007199254740993", "&reason=" + strings.Repeat("x", api.ProductControlOwnerFormMaxBytes)} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(base.Encode()+extra))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if _, err := ownerParseReview(httptest.NewRecorder(), r); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("form accepted: %v", err)
		}
	}
	if got := ownerMoney("EUR", 9223372036854775807); got != "EUR 92233720368547.75807" {
		t.Fatalf("integer precision: %s", got)
	}
}
