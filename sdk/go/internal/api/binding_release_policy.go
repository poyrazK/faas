package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const CodeBindingReleaseRequired = "bindings_release_required"
const CodeBindingReleasePolicyChanged = "bindings_release_policy_changed"

// BindingReleasePolicy applies to every traffic increase in one deployment scope.
type BindingReleasePolicy struct {
	AppID                 string     `json:"app_id"`
	Scope                 string     `json:"scope"`
	Mode                  string     `json:"mode"`
	Revision              int64      `json:"revision"`
	MaxVerificationAge    string     `json:"max_verification_age"`
	RequireApplicationAck bool       `json:"require_application_ack"`
	UpdatedAt             *time.Time `json:"updated_at,omitempty"`
	Reason                string     `json:"reason,omitempty"`
}

type SetBindingReleasePolicyRequest struct {
	Mode                  string `json:"mode"`
	ExpectedRevision      *int64 `json:"expected_revision"`
	MaxVerificationAge    string `json:"max_verification_age,omitempty"`
	RequireApplicationAck bool   `json:"require_application_ack"`
	Reason                string `json:"reason,omitempty"`
}

func ValidateBindingReleasePolicyRequest(r SetBindingReleasePolicyRequest) error {
	if (r.Mode != "off" && r.Mode != "enforce") || r.ExpectedRevision == nil || *r.ExpectedRevision < 0 || *r.ExpectedRevision >= BindingReleasePolicyMaxRevision {
		return fmt.Errorf("supply mode off or enforce and expected_revision (0 initially)")
	}
	if len(r.Reason) > BindingReleasePolicyReasonMaxBytes || !utf8.ValidString(r.Reason) || strings.IndexFunc(r.Reason, unicode.IsControl) >= 0 {
		return fmt.Errorf("reason must be one line of at most %d bytes", BindingReleasePolicyReasonMaxBytes)
	}
	if r.Mode == "off" && strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("a reason is required to disable release enforcement")
	}
	if r.MaxVerificationAge != "" {
		age, err := time.ParseDuration(r.MaxVerificationAge)
		if err != nil || age < time.Second || age > BindingReleasePolicyMaxAge || age%time.Second != 0 {
			return fmt.Errorf("max_verification_age must be a whole number of seconds between 1s and 24h")
		}
	}
	return nil
}

func bindingReleasePolicyPath(slug, scope string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/bindings/release-policy?scope=" + url.QueryEscape(scope)
}
func (c *Client) GetBindingReleasePolicy(ctx context.Context, slug, scope string) (BindingReleasePolicy, error) {
	var result BindingReleasePolicy
	err := c.do(ctx, http.MethodGet, bindingReleasePolicyPath(slug, scope), nil, &result)
	return result, err
}
func (c *Client) SetBindingReleasePolicy(ctx context.Context, slug, scope string, request SetBindingReleasePolicyRequest) (BindingReleasePolicy, error) {
	var result BindingReleasePolicy
	err := c.do(ctx, http.MethodPut, bindingReleasePolicyPath(slug, scope), request, &result)
	return result, err
}
