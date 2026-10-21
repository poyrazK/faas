package paddleinvoice

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Address is a Paddle customer's postal address (ADR-20261020120000). A
// transaction with an address can be billed.
type Address struct {
	ID          string    `json:"id"`
	Status      string    `json:"status"`
	FirstLine   string    `json:"first_line"`
	SecondLine  string    `json:"second_line"`
	City        string    `json:"city"`
	PostalCode  string    `json:"postal_code"`
	Region      string    `json:"region"`
	CountryCode string    `json:"country_code"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Business is a Paddle customer's company and tax identifier.
type Business struct {
	ID            string    `json:"id"`
	Status        string    `json:"status"`
	Name          string    `json:"name"`
	TaxIdentifier string    `json:"tax_identifier"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// nullable sends an empty field as null so Paddle clears it.
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func customerPath(customerID, kind, id string) string {
	path := "/customers/" + url.PathEscape(customerID) + "/" + kind
	if id != "" {
		path += "/" + url.PathEscape(id)
	}
	return path
}

// newestActive is the query for a customer's newest active address or
// business. Paddle orders these lists only by ID, and its IDs grow with
// creation time (ADR-20261020120000 amendment 1).
const newestActive = "?status=active&order_by=id%5BDESC%5D&per_page=1"

// ActiveAddress returns the customer's newest active address; ok is false
// when the customer has none.
func (c *Client) ActiveAddress(ctx context.Context, key, customerID string) (Address, bool, error) {
	var out struct {
		Data []Address `json:"data"`
	}
	if err := c.do(ctx, key, http.MethodGet, customerPath(customerID, "addresses", "")+newestActive, nil, &out); err != nil || len(out.Data) == 0 {
		return Address{}, false, err
	}
	return out.Data[0], true, nil
}

// PutAddress updates the address with a.ID, or creates one when it is empty.
func (c *Client) PutAddress(ctx context.Context, key, customerID string, a Address) (Address, error) {
	body := map[string]any{"first_line": nullable(a.FirstLine), "second_line": nullable(a.SecondLine), "city": nullable(a.City),
		"postal_code": nullable(a.PostalCode), "region": nullable(a.Region), "country_code": a.CountryCode}
	method := http.MethodPost
	if a.ID != "" {
		method = http.MethodPatch
	}
	var out struct {
		Data Address `json:"data"`
	}
	err := c.do(ctx, key, method, customerPath(customerID, "addresses", a.ID), body, &out)
	return out.Data, err
}

// ActiveBusiness returns the customer's newest active business; ok is false
// when the customer has none.
func (c *Client) ActiveBusiness(ctx context.Context, key, customerID string) (Business, bool, error) {
	var out struct {
		Data []Business `json:"data"`
	}
	if err := c.do(ctx, key, http.MethodGet, customerPath(customerID, "businesses", "")+newestActive, nil, &out); err != nil || len(out.Data) == 0 {
		return Business{}, false, err
	}
	return out.Data[0], true, nil
}

// PutBusiness updates the business with b.ID, or creates one when it is
// empty.
func (c *Client) PutBusiness(ctx context.Context, key, customerID string, b Business) (Business, error) {
	body := map[string]any{"name": b.Name, "tax_identifier": nullable(b.TaxIdentifier)}
	method := http.MethodPost
	if b.ID != "" {
		method = http.MethodPatch
	}
	var out struct {
		Data Business `json:"data"`
	}
	err := c.do(ctx, key, method, customerPath(customerID, "businesses", b.ID), body, &out)
	return out.Data, err
}
