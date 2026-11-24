package api

import (
	"context"
	"net/url"
	"time"
)

// ConsumerCreditPack is a fixed amount of prepaid statement credit. Checkout
// charges the same amount in the pack's currency.
type ConsumerCreditPack struct {
	ID               string `json:"id"`
	Label            string `json:"label"`
	Currency         string `json:"currency"`
	AmountMillicents int64  `json:"amount_millicents"`
}

// ConsumerCreditPackSettings defines what an app sells and where Stripe sends
// the buyer back. An empty pack list disables new purchases.
type ConsumerCreditPackSettings struct {
	ReturnURL string               `json:"return_url,omitempty"`
	Packs     []ConsumerCreditPack `json:"packs"`
}

type ConsumerCreditPacksResponse struct {
	Packs []ConsumerCreditPack `json:"packs"`
}

type CreateConsumerCreditPurchaseRequest struct {
	PurchaseID string `json:"purchase_id"`
	PackID     string `json:"pack_id"`
}

type ConsumerCreditPurchaseResponse struct {
	ID               string    `json:"id"`
	PackID           string    `json:"pack_id"`
	Currency         string    `json:"currency"`
	AmountMillicents int64     `json:"amount_millicents"`
	Status           string    `json:"status"`
	ExpiresAt        time.Time `json:"expires_at"`
	CheckoutURL      string    `json:"checkout_url,omitempty"`
	CreditGrantID    string    `json:"credit_grant_id,omitempty"`
}

func (c *Client) GetConsumerCreditPackSettings(ctx context.Context, app string) (ConsumerCreditPackSettings, error) {
	var out ConsumerCreditPackSettings
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/consumer-credit-packs", nil, &out)
}

func (c *Client) PutConsumerCreditPackSettings(ctx context.Context, app string, settings ConsumerCreditPackSettings) (ConsumerCreditPackSettings, error) {
	var out ConsumerCreditPackSettings
	return out, c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(app)+"/consumer-credit-packs", settings, &out)
}

// ListConsumerCreditPacks uses the customer's billing-scope consumer key.
func (c *Client) ListConsumerCreditPacks(ctx context.Context, app string) (ConsumerCreditPacksResponse, error) {
	var out ConsumerCreditPacksResponse
	return out, c.do(ctx, "GET", "/v1/consumer-billing/"+url.PathEscape(app)+"/credit-packs", nil, &out)
}

// CreateConsumerCreditPurchase starts or resumes a customer checkout.
func (c *Client) CreateConsumerCreditPurchase(ctx context.Context, app string, req CreateConsumerCreditPurchaseRequest) (ConsumerCreditPurchaseResponse, error) {
	var out ConsumerCreditPurchaseResponse
	return out, c.do(ctx, "POST", "/v1/consumer-billing/"+url.PathEscape(app)+"/credit-purchases", req, &out)
}

func (c *Client) GetConsumerCreditPurchase(ctx context.Context, app, id string) (ConsumerCreditPurchaseResponse, error) {
	var out ConsumerCreditPurchaseResponse
	return out, c.do(ctx, "GET", "/v1/consumer-billing/"+url.PathEscape(app)+"/credit-purchases/"+url.PathEscape(id), nil, &out)
}
