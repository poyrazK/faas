package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ServiceBindingExplanation is a read-only policy and routing observation.
// Allowed does not establish live caller identity, guest adoption or connectivity.
type ServiceBindingExplanation struct {
	Caller             string                      `json:"caller"`
	Target             string                      `json:"target"`
	Method             string                      `json:"method"`
	Path               string                      `json:"path"`
	Result             string                      `json:"result"`
	Scope              string                      `json:"scope"`
	CheckedAt          time.Time                   `json:"checked_at"`
	CallerFingerprint  string                      `json:"caller_fingerprint,omitempty"`
	TargetFingerprint  string                      `json:"target_fingerprint,omitempty"`
	CallerDeploymentID string                      `json:"caller_deployment_id,omitempty"`
	TargetAppID        string                      `json:"target_app_id,omitempty"`
	TargetDeploymentID string                      `json:"target_deployment_id,omitempty"`
	ReleaseID          string                      `json:"release_id,omitempty"`
	PolicySource       string                      `json:"policy_source,omitempty"`
	Transport          ServiceBindingTransport     `json:"canonical_transport,omitempty"`
	Checks             []ServiceAuthorizationCheck `json:"checks"`
	Limitations        string                      `json:"limitations"`
}

type ServiceAuthorizationCheck struct {
	Rule    string `json:"rule"`
	Result  string `json:"result"`
	Reason  string `json:"reason"`
	Setting string `json:"setting,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

type ExplainServiceBindingOptions struct {
	DeploymentID string
	Scope        string
	Method       string
	Path         string
}

// NormalizeServiceExplanationRequest shares the gateway's decoded path matcher.
// Query values are accepted but omitted from the returned public path.
func NormalizeServiceExplanationRequest(method, path string) (string, string, error) {
	method = strings.ToUpper(strings.TrimSpace(method))
	_, err := NormalizeServiceCallerScopes(ServiceCallerScopes{"caller": {Methods: []string{method}, PathPrefixes: []string{"/"}}})
	if err != nil {
		return "", "", fmt.Errorf("invalid HTTP method: %w", err)
	}
	requestURI, err := NormalizeServiceBindingSmokePath(path)
	if err != nil {
		return "", "", err
	}
	parsed, err := url.ParseRequestURI(requestURI)
	if err != nil {
		return "", "", err
	}
	if !(ServiceCallScope{Methods: []string{"*"}, PathPrefixes: []string{"/"}}).Allows(method, parsed.Path) {
		return "", "", errors.New("path must be unambiguous, without dot segments, repeated separators, or backslashes")
	}
	return method, parsed.Path, nil
}

func (c *Client) ExplainAppServiceBinding(ctx context.Context, slug, service string, in ExplainServiceBindingOptions) (ServiceBindingExplanation, error) {
	var out ServiceBindingExplanation
	query := url.Values{"deployment_id": {in.DeploymentID}}
	if in.Scope != "" {
		query.Set("scope", in.Scope)
	}
	if in.Method != "" {
		query.Set("method", in.Method)
	}
	if in.Path != "" {
		query.Set("path", in.Path)
	}
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/bindings/services/"+url.PathEscape(service)+"/explanation?"+query.Encode(), nil, &out)
	return out, err
}
