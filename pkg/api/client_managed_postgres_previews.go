package api

import (
	"context"
	"net/http"
	"net/url"
)

// SetManagedPostgresPreviews sets what a database gives PR preview
// environments: "off", "empty" or "copy" (ADR-20261102123523).
func (c *Client) SetManagedPostgresPreviews(ctx context.Context, id, previews string) (ManagedPostgresDatabase, error) {
	var out ManagedPostgresDatabase
	err := c.do(ctx, http.MethodPut, "/v1/postgres/databases/"+url.PathEscape(id)+"/previews", SetManagedPostgresPreviewsRequest{Previews: previews}, &out)
	return out, err
}
