package paddle

import (
	"context"
	"errors"
	"fmt"

	"github.com/PaddleHQ/paddle-go-sdk/v5"
	"github.com/onebox-faas/faas/pkg/state"
)

// CreateCustomer: POST /customers with the account email +
// custom_data.faas_account_id. Returns the ctm_… ID; the caller
// (apid) writes it back via state.Store.UpdateAccountStripeCustomerID
// — column name is intentionally stale per ADR-025.
//
// Idempotency strategy: Paddle's Customers endpoint does not
// support Idempotency-Key today. The PR #3 apid dispatch will
// guard against double-Create by checking the existing
// accounts.stripe_customer_id column first — a second call with
// the ID already set is a no-op.
func (p *Provider) CreateCustomer(ctx context.Context, acct state.Account) (string, error) {
	if p.client == nil {
		return "", fmt.Errorf("paddle: SDK not initialized (apiKey=%q)", redactAPIKey(p.apiKey))
	}
	if acct.Email == "" {
		return "", errors.New("paddle: CreateCustomer requires acct.Email")
	}

	req := &paddle.CreateCustomerRequest{
		Email: acct.Email,
		CustomData: paddle.CustomData{
			"faas_account_id": acct.ID,
		},
	}
	cust, err := p.client.CreateCustomer(ctx, req)
	if err != nil {
		return "", fmt.Errorf("paddle: CreateCustomer account=%s: %w", acct.ID, err)
	}
	if cust == nil || cust.ID == "" {
		return "", fmt.Errorf("paddle: CreateCustomer returned empty ID for account=%s", acct.ID)
	}
	p.log.Info("paddle: CreateCustomer ok", "account", acct.ID, "paddle_customer", cust.ID)
	return cust.ID, nil
}
