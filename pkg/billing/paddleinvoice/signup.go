package paddleinvoice

import (
	"context"
	"net/http"
	"net/url"
)

// CustomerByEmail returns the account's active Paddle customer with email,
// creating it when there is none, for a self-serve signup
// (ADR-20261022100735). Paddle keeps one customer per email.
func (c *Client) CustomerByEmail(ctx context.Context, key, email, name string) (string, error) {
	var found struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.do(ctx, key, http.MethodGet, "/customers?status=active&per_page=1&email="+url.QueryEscape(email), nil, &found); err != nil {
		return "", err
	}
	if len(found.Data) > 0 && found.Data[0].ID != "" {
		return found.Data[0].ID, nil
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	err := c.do(ctx, key, http.MethodPost, "/customers", map[string]any{"email": email, "name": nullable(name)}, &created)
	return created.Data.ID, err
}
