package api

import (
	"context"
	"encoding/json"
	"net/url"
)

// AppOpenAPIPolicyPreviewResponse is the read-only declared-vs-observed
// contract preview for an app: each route row includes the edge rules that
// match it, so contract drift and policy coverage show before any rule
// changes (ADR-20261028110044 adds it to the Go SDK).
type AppOpenAPIPolicyPreviewResponse struct {
	AppID                      string                         `json:"app_id"`
	Source                     string                         `json:"source"`
	ObservedAvailable          bool                           `json:"observed_available"`
	ObservedSource             string                         `json:"observed_source"`
	ObservedInventoryAvailable bool                           `json:"observed_inventory_available"`
	ObservedCapHit             bool                           `json:"observed_cap_hit"`
	CollectorsExpected         int                            `json:"collectors_expected"`
	CollectorsHealthy          int                            `json:"collectors_healthy"`
	OpenAPIVersion             string                         `json:"openapi_version,omitempty"`
	Routes                     []AppOpenAPIPolicyPreviewRoute `json:"routes"`
	Suggestions                []EdgeRuleSuggestion           `json:"suggestions,omitempty"`
}

// AppOpenAPIPolicyPreviewRoute is one path/method row in the policy preview.
// Status is one of matched, declared_only, or observed_only.
type AppOpenAPIPolicyPreviewRoute struct {
	Path     string                        `json:"path"`
	Method   string                        `json:"method"`
	Status   string                        `json:"status"`
	Declared bool                          `json:"declared"`
	Observed bool                          `json:"observed"`
	Covered  bool                          `json:"covered"`
	Rules    []AppOpenAPIPolicyPreviewRule `json:"rules,omitempty"`
}

// AppOpenAPIPolicyPreviewRule is the stable subset of an edge rule attached
// to a preview route or created by an apply. Action is the rule's opaque JSON
// action object.
type AppOpenAPIPolicyPreviewRule struct {
	ID           string          `json:"id"`
	MatchHost    string          `json:"match_host"`
	MatchPath    string          `json:"match_path"`
	MatchMethods []string        `json:"match_methods"`
	Priority     int             `json:"priority"`
	Enabled      bool            `json:"enabled"`
	Kind         string          `json:"kind"`
	ValidateMode string          `json:"validate_mode,omitempty"`
	Action       json.RawMessage `json:"action"`
}

// EdgeRuleSuggestion is one validation rule an apply would create for an
// uncovered OpenAPI operation.
type EdgeRuleSuggestion struct {
	Path    string         `json:"path"`
	Methods []string       `json:"methods"`
	Kind    string         `json:"kind"`
	Action  map[string]any `json:"action"`
}

// ApplyAppOpenAPIPolicyRequest plans (Confirm false) or applies the
// validation rules derived from the app's OpenAPI document. Confirm requires
// the plan's PreviewSHA256; MatchHost defaults to the platform hostname.
type ApplyAppOpenAPIPolicyRequest struct {
	Confirm       bool   `json:"confirm,omitempty"`
	PreviewSHA256 string `json:"preview_sha256,omitempty"`
	MatchHost     string `json:"match_host,omitempty"`
}

// AppOpenAPIPolicyApplyResponse is a read-only plan when Planned is true;
// otherwise Applied lists the rules this call created (none for an
// idempotent no-op). Read a full rule with the edge rule API.
type AppOpenAPIPolicyApplyResponse struct {
	AppID         string                        `json:"app_id"`
	MatchHost     string                        `json:"match_host"`
	PreviewSHA256 string                        `json:"preview_sha256"`
	Suggestions   []EdgeRuleSuggestion          `json:"suggestions"`
	Planned       bool                          `json:"planned"`
	Applied       []AppOpenAPIPolicyPreviewRule `json:"applied"`
	AppliedCount  int                           `json:"applied_count"`
}

// PreviewAppOpenAPIPolicy returns the declared-vs-observed route preview for
// an app. It never changes the document or policy.
func (c *PreviewClient) PreviewAppOpenAPIPolicy(ctx context.Context, slug string) (AppOpenAPIPolicyPreviewResponse, error) {
	var out AppOpenAPIPolicyPreviewResponse
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/openapi/preview", nil, &out)
	return out, err
}

// ApplyAppOpenAPIPolicy plans, or with Confirm and the plan's PreviewSHA256
// applies, the validation rules for the app's uncovered OpenAPI operations.
// A stale token returns 409 openapi_policy_stale and writes nothing.
func (c *PreviewClient) ApplyAppOpenAPIPolicy(ctx context.Context, slug string, req ApplyAppOpenAPIPolicyRequest) (AppOpenAPIPolicyApplyResponse, error) {
	var out AppOpenAPIPolicyApplyResponse
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/openapi/apply", req, &out)
	return out, err
}
