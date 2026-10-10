package api

import (
	"context"
	"errors"
	"net/url"
)

// ValidateDurableEntityRestore executes application code; it needs mutation
// scopes and consumes normal invocation resources. It commits no entity state.
func (c *Client) ValidateDurableEntityRestore(ctx context.Context, slug string, request DurableEntityRestoreRequest) (DurableEntityRestoreValidationResponse, error) {
	var out DurableEntityRestoreValidationResponse
	if request.Namespace == "" || request.Key == "" || request.RequestID == "" || request.ExpectedVersion == 0 {
		return out, errors.New("restore validation requires selectors, request_id and expected_version")
	}
	return out, c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/entities/restore/validate", request, &out)
}
