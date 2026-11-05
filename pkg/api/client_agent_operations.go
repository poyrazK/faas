package api

import (
	"context"
	"net/http"
	"net/url"
)

// PutAppEnvInScope changes a non-secret runtime variable without granting
// access to the sealed secrets surface. Native config guards remain enabled.
func (c *Client) PutAppEnvInScope(ctx context.Context, app, scope, key, value string) error {
	return c.do(ctx, http.MethodPut, c.configWriteQuery("/v1/apps/"+url.PathEscape(app)+"/env/"+url.PathEscape(key), scope), PutAppEnvRequest{Value: value}, nil)
}
