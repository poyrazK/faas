package api

import (
	"context"
	"net/url"
)

// ExportAppEnvRequest requires an explicit acknowledgement of plaintext values.
// It is accepted only by POST /v1/apps/{slug}/env-export, never by metadata GETs.
type ExportAppEnvRequest struct {
	AcknowledgeSensitiveValues bool `json:"acknowledge_sensitive_values"`
}

func (r ExportAppEnvRequest) Validate() *Problem {
	if !r.AcknowledgeSensitiveValues {
		return ErrValidation("acknowledge_sensitive_values must be true to export plaintext environment values")
	}
	return nil
}

// AppEnvExportResponse contains mutable plaintext env only, in one scope.
// Sealed secrets, deployment manifests and image defaults are never read.
// Responses must be no-store and are not eligible for idempotency persistence.
type AppEnvExportResponse struct {
	AppSlug string            `json:"app_slug"`
	Scope   string            `json:"scope"`
	Values  map[string]string `json:"values"`
}

// ExportAppEnv explicitly exports plaintext env values from one scope.
// The caller must acknowledge sensitive values; metadata listing never exports them.
func (c *Client) ExportAppEnv(ctx context.Context, slug, scope string, request ExportAppEnvRequest) (AppEnvExportResponse, error) {
	var out AppEnvExportResponse
	if problem := request.Validate(); problem != nil {
		return out, problem
	}
	path := "/v1/apps/" + url.PathEscape(slug) + "/env-export"
	if scope != "" {
		path += "?" + url.Values{"scope": {scope}}.Encode()
	}
	err := c.do(ctx, "POST", path, request, &out)
	return out, err
}
