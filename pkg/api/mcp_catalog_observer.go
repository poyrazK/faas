package api

import (
	"context"
	"net/url"
	"strings"
	"time"
)

// MCPCatalogObserverConfig is opt-in intent for an apid-managed catalog observer.
type MCPCatalogObserverConfig struct {
	Enabled         bool                           `json:"enabled"`
	Paused          bool                           `json:"paused"`
	IntervalSeconds int                            `json:"interval_seconds"`
	TimeoutSeconds  int                            `json:"timeout_seconds"`
	Endpoint        string                         `json:"endpoint"`
	SecretRef       string                         `json:"secret_ref,omitempty"`
	OAuth           *MCPCatalogObserverOAuthConfig `json:"oauth,omitempty"`
}

// MCPCatalogObserverOAuthConfig binds one role to an explicit OAuth refresh grant.
// Only references and public client configuration are accepted, never plaintext tokens.
type MCPCatalogObserverOAuthConfig struct {
	RevocationEndpoint    string   `json:"revocation_endpoint,omitempty"`
	RevocationAuthMethod  string   `json:"revocation_auth_method,omitempty"`
	TokenEndpoint         string   `json:"token_endpoint"`
	ClientID              string   `json:"client_id"`
	RefreshTokenSecretRef string   `json:"refresh_token_secret_ref"`
	ClientSecretRef       string   `json:"client_secret_ref,omitempty"`
	Guided                bool     `json:"guided,omitempty"`
	AuthorizationEndpoint string   `json:"authorization_endpoint,omitempty"`
	Scopes                []string `json:"scopes,omitempty"`
	Profile               string   `json:"profile,omitempty"`
	TokenAuthMethod       string   `json:"token_auth_method,omitempty"`
	BrowserRedirectURI    string   `json:"browser_redirect_uri,omitempty"`
	CallbackPort          int      `json:"callback_port,omitempty"`
	Resource              string   `json:"resource,omitempty"`
	Issuer                string   `json:"issuer,omitempty"`
	DiscoveryURL          string   `json:"discovery_url,omitempty"`
}

// MCPCatalogObserverOAuthStatus exposes renewal health. Private sealed data is
// retained in storage only and removed by CatalogObserverStatus before responses.
type MCPCatalogObserverOAuthStatus struct {
	RetirementID          string     `json:"retirement_id,omitempty"`
	State                 string     `json:"state"`
	ExpiresAt             *time.Time `json:"expires_at,omitempty"`
	LastRenewalAt         *time.Time `json:"last_renewal_at,omitempty"`
	Ciphertext            []byte     `json:"ciphertext,omitempty"`
	SourceSHA256          string     `json:"source_sha256,omitempty"`
	ReauthorizationNeeded bool       `json:"reauthorization_needed,omitempty"`
	GrantedScopes         []string   `json:"granted_scopes,omitempty"`
}

// MCPCatalogObserverStatus contains bounded runtime metadata, never credentials or source errors.
type MCPCatalogObserverStatus struct {
	State               string                         `json:"state"`
	LastAttemptAt       *time.Time                     `json:"last_attempt_at,omitempty"`
	LastSuccessAt       *time.Time                     `json:"last_success_at,omitempty"`
	NextCheckAt         *time.Time                     `json:"next_check_at,omitempty"`
	FailureReason       string                         `json:"failure_reason,omitempty"`
	ConsecutiveFailures int                            `json:"consecutive_failures"`
	LeaseID             string                         `json:"lease_id,omitempty"`
	LeaseUntil          *time.Time                     `json:"lease_until,omitempty"`
	OAuth               *MCPCatalogObserverOAuthStatus `json:"oauth,omitempty"`
	Renewal             *MCPCatalogOAuthRenewal        `json:"oauth_renewal,omitempty"`
	OAuthFlow           *MCPCatalogOAuthFlow           `json:"oauth_flow,omitempty"`
}

func (c *MCPCatalogObserverConfig) NormalizeAndValidate(freshness int, tokenEnv string) *Problem {
	if c.IntervalSeconds == 0 {
		c.IntervalSeconds = MCPCatalogObserverDefaultIntervalSeconds
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = MCPCatalogObserverDefaultTimeoutSeconds
	}
	if c.Endpoint == "" {
		c.Endpoint = "/mcp"
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || !strings.HasPrefix(c.Endpoint, "/") || strings.HasPrefix(c.Endpoint, "//") || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(c.Endpoint, "\\\r\n") || len(c.Endpoint) > MCPCatalogObserverMaxEndpointBytes {
		return ErrValidation("managed observer endpoint must be an app-relative path without query, fragment or authority")
	}
	if c.SecretRef != "" && (!strings.HasPrefix(c.SecretRef, SecretRefPrefix) || ValidateEnvKey(strings.TrimPrefix(c.SecretRef, SecretRefPrefix)) != nil) {
		return ErrValidation("managed observer secret_ref must name an app secret as secret:NAME")
	}
	if c.IntervalSeconds < MCPCatalogObserverMinIntervalSeconds || c.IntervalSeconds > MCPCatalogObserverMaxIntervalSeconds || c.TimeoutSeconds < 1 || c.TimeoutSeconds > MCPCatalogObserverMaxTimeoutSeconds {
		return ErrValidation("managed observer interval must be 30–3600 seconds and timeout 1–30 seconds")
	}
	if c.IntervalSeconds+c.IntervalSeconds/10+MCPCatalogObserverLeaseSeconds+MCPCatalogObserverPollSeconds >= freshness {
		return ErrValidation("managed observer interval, jitter, poll and lease budget must fit below freshness_seconds")
	}
	if c.OAuth != nil {
		if problem := c.OAuth.Validate(); problem != nil {
			return problem
		}
		if c.SecretRef != "" {
			return ErrValidation("oauth and secret_ref are mutually exclusive")
		}
	}
	if tokenEnv != "" {
		return ErrValidation("managed observers use secret_ref; token_env is for local observers")
	}
	return nil
}
func CatalogObserverStatus(snapshot MCPCatalogMonitorSnapshot, at time.Time) *MCPCatalogObserverStatus {
	if snapshot.Config.Observer == nil {
		return nil
	}
	status := MCPCatalogObserverStatus{State: "scheduled"}
	if snapshot.ObserverStatus != nil {
		status = *snapshot.ObserverStatus
	}
	switch {
	case snapshot.Config.Observer.Paused:
		status.State = "paused"
	case !snapshot.Config.Enabled || !snapshot.Config.Observer.Enabled:
		status.State = "stopped"
	case status.LeaseUntil != nil && status.LeaseUntil.After(at):
		status.State = "checking"
	case status.FailureReason != "":
		status.State = "degraded"
	case status.NextCheckAt != nil && at.Sub(*status.NextCheckAt) > time.Duration(MCPCatalogObserverPollSeconds)*time.Second:
		status.State = "overdue"
	case status.State == "checking":
		status.State = "overdue"
	default:
		status.State = "scheduled"
	}
	// A lease token is internal fencing metadata, not an API credential.
	status.LeaseID = ""
	status.OAuthFlow = nil
	if status.Renewal != nil {
		copy := *status.Renewal
		copy.ID, copy.SourceSHA256, copy.LeaseID = "", "", ""
		copy.Result = nil
		status.Renewal = &copy
	}
	if status.OAuth != nil {
		oauth := *status.OAuth
		oauth.Ciphertext = nil
		oauth.SourceSHA256 = ""
		oauth.RetirementID = ""
		oauth.ReauthorizationNeeded = oauth.State == "reauthorization_required" || (status.Renewal != nil && status.Renewal.State == "reauthorization_required") || (status.FailureReason == "credential_unavailable" && oauth.ExpiresAt != nil && !oauth.ExpiresAt.After(at.Add(time.Duration(MCPCatalogOAuthReminderSeconds)*time.Second)))
		status.OAuth = &oauth
	}
	return &status
}

func (c *MCPCatalogObserverOAuthConfig) Validate() *Problem {
	if c.RevocationEndpoint != "" && !ValidMCPOAuthHTTPSURL(c.RevocationEndpoint) {
		return ErrValidation("revocation_endpoint must be an explicit HTTPS URL")
	}
	if c.RevocationAuthMethod != "" && c.RevocationAuthMethod != "none" && c.RevocationAuthMethod != "client_secret_basic" && c.RevocationAuthMethod != "client_secret_post" {
		return ErrValidation("invalid revocation_auth_method")
	}
	if c.RevocationAuthMethod != "" && c.RevocationEndpoint == "" {
		return ErrValidation("revocation_auth_method requires revocation_endpoint")
	}
	if c.RevocationEndpoint != "" && c.ResolvedRevocationAuthMethod() != "none" && c.ClientSecretRef == "" {
		return ErrValidation("revocation authentication requires client_secret_ref")
	}
	u, err := url.Parse(c.TokenEndpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" || len(c.TokenEndpoint) > MCPCatalogObserverMaxEndpointBytes || strings.ContainsAny(c.TokenEndpoint, "\\\r\n") {
		return ErrValidation("OAuth token_endpoint must be an explicit HTTPS URL without credentials, query, fragment or port")
	}
	if c.ClientID == "" || len(c.ClientID) > MCPCatalogOAuthMaxClientIDBytes || strings.ContainsAny(c.ClientID, "\r\n") {
		return ErrValidation("invalid OAuth client_id")
	}
	if c.Guided {
		endpoint := MCPCatalogObserverOAuthConfig{TokenEndpoint: c.AuthorizationEndpoint, ClientID: c.ClientID, RefreshTokenSecretRef: "secret:CHECK"}
		if c.RefreshTokenSecretRef != "" || endpoint.Validate() != nil {
			return ErrValidation("guided OAuth requires an explicit HTTPS authorization_endpoint and no refresh_token_secret_ref")
		}
	}
	if problem := c.ValidateCompatibility(); problem != nil {
		return problem
	}
	if len(c.RequestedScopes()) > MCPCatalogOAuthMaxScopes {
		return ErrValidation("too many OAuth scopes")
	}
	for _, scope := range c.Scopes {
		if !ValidMCPOAuthScope(scope) {
			return ErrValidation("invalid OAuth scope")
		}
	}
	for _, ref := range []string{c.RefreshTokenSecretRef, c.ClientSecretRef} {
		if ref == "" && (c.Guided || (ref == c.ClientSecretRef && c.RefreshTokenSecretRef != "")) {
			continue
		}
		if !strings.HasPrefix(ref, SecretRefPrefix) || ValidateEnvKey(strings.TrimPrefix(ref, SecretRefPrefix)) != nil {
			return ErrValidation("OAuth credentials must reference app secrets as secret:NAME")
		}
	}
	return nil
}

// MCPCatalogOAuthFlow is private, expiring authorization state sealed by apid.
// Public projections remove this entire value.
type MCPCatalogOAuthFlow struct {
	BrowserSessionSHA256 string    `json:"browser_session_sha256,omitempty"`
	StateSHA256          string    `json:"state_sha256"`
	Ciphertext           []byte    `json:"ciphertext"`
	ExpiresAt            time.Time `json:"expires_at"`
	Consumed             bool      `json:"consumed"`
}
type MCPCatalogOAuthRequest struct {
	Action      string `json:"action"`
	Role        string `json:"role"`
	Revision    string `json:"revision"`
	RedirectURI string `json:"redirect_uri,omitempty"`
	State       string `json:"state,omitempty"`
	Code        string `json:"code,omitempty"`
	Denied      bool   `json:"denied,omitempty"`
}
type MCPCatalogOAuthResponse struct {
	Revision           string                 `json:"revision"`
	State              string                 `json:"state"`
	AuthorizationURL   string                 `json:"authorization_url,omitempty"`
	CallbackState      string                 `json:"callback_state,omitempty"`
	ProviderRevocation string                 `json:"provider_revocation,omitempty"`
	Checks             []MCPCatalogOAuthCheck `json:"checks,omitempty"`
}

func ValidMCPOAuthScope(scope string) bool {
	if len(scope) == 0 || len(scope) > MCPCatalogOAuthMaxScopeBytes {
		return false
	}
	for _, c := range scope {
		if c < 0x21 || c > 0x7e || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}
func (c *Client) MCPCatalogOAuth(ctx context.Context, slug string, req MCPCatalogOAuthRequest) (MCPCatalogOAuthResponse, error) {
	var out MCPCatalogOAuthResponse
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/mcp-catalog-monitor/oauth", req, &out)
	return out, err
}

// MCPCatalogOAuthCheck reports bounded public metadata checks, never source errors.
type MCPCatalogOAuthCheck struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

const MCPCatalogOAuthBrowserCallbackPath = "/dashboard/mcp/oauth/callback"

func ValidMCPOAuthBrowserRedirect(value string) bool {
	u, err := url.Parse(value)
	return err == nil && ValidMCPOAuthHTTPSURL(value) && u.Path == MCPCatalogOAuthBrowserCallbackPath && u.RawPath == ""
}

func (c *MCPCatalogObserverOAuthConfig) ValidateCompatibility() *Problem {
	if c.BrowserRedirectURI != "" && (!c.Guided || !ValidMCPOAuthBrowserRedirect(c.BrowserRedirectURI)) {
		return ErrValidation("browser_redirect_uri requires guided mode and an explicit HTTPS console MCP callback URL")
	}
	if c.Profile != "" && c.Profile != "generic" && c.Profile != "google" && c.Profile != "oidc" {
		return ErrValidation("OAuth profile must be generic, google or oidc")
	}
	if c.CallbackPort < 0 || c.CallbackPort > MCPCatalogOAuthMaxCallbackPort {
		return ErrValidation("OAuth callback_port must be 0 (dynamic) or 1–65535")
	}
	method := c.ResolvedTokenAuthMethod()
	if method != "none" && method != "client_secret_basic" && method != "client_secret_post" {
		return ErrValidation("unsupported OAuth token_auth_method; use none, client_secret_basic or client_secret_post")
	}
	if (method == "none") != (c.ClientSecretRef == "") {
		return ErrValidation("OAuth token authentication must match presence of client_secret_ref")
	}
	for _, value := range []string{c.Resource, c.Issuer, c.DiscoveryURL} {
		if value != "" && !ValidMCPOAuthHTTPSURL(value) {
			return ErrValidation("OAuth resource, issuer and discovery_url must be explicit HTTPS URLs without credentials, query, fragment or port")
		}
	}
	if (c.Issuer == "") != (c.DiscoveryURL == "") {
		return ErrValidation("OAuth issuer and discovery_url must be configured together")
	}
	if !c.Guided && (c.CallbackPort != 0 || c.DiscoveryURL != "" || c.Profile == "oidc" || c.Profile == "google") {
		return ErrValidation("OAuth browser profiles, callback ports and discovery require guided mode")
	}
	return nil
}
func ValidMCPOAuthHTTPSURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.Port() == "" && len(value) <= MCPCatalogObserverMaxEndpointBytes && !strings.ContainsAny(value, "\\\r\n")
}
func (c *MCPCatalogObserverOAuthConfig) ResolvedTokenAuthMethod() string {
	if c.TokenAuthMethod != "" {
		return c.TokenAuthMethod
	}
	if c.ClientSecretRef == "" {
		return "none"
	}
	if c.Profile == "google" {
		return "client_secret_post"
	}
	return "client_secret_basic"
}
func (c *MCPCatalogObserverOAuthConfig) RequestedScopes() []string {
	scopes := append([]string(nil), c.Scopes...)
	if c.Profile == "oidc" {
		for _, required := range []string{"openid", "offline_access"} {
			found := false
			for _, scope := range scopes {
				if scope == required {
					found = true
				}
			}
			if !found {
				scopes = append(scopes, required)
			}
		}
	}
	return scopes
}

func (c *MCPCatalogObserverOAuthConfig) ResolvedRevocationAuthMethod() string {
	if c.RevocationAuthMethod != "" {
		return c.RevocationAuthMethod
	}
	if c.Profile == "google" {
		return "none"
	}
	return c.ResolvedTokenAuthMethod()
}

// Lifecycle survives observer configuration changes. Only its public projection
// may cross the API boundary; sealed grants and fencing tokens remain private.
type MCPCatalogOAuthLifecycle struct {
	LocalState      string                     `json:"local_state"`
	CredentialState string                     `json:"credential_state,omitempty"`
	Revocation      *MCPCatalogOAuthRevocation `json:"revocation,omitempty"`
	History         []MCPCatalogOAuthEvent     `json:"history"`
}
type MCPCatalogOAuthEvent struct {
	ID   string    `json:"id"`
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
}
type MCPCatalogOAuthRevocation struct {
	ID                  string                         `json:"id"`
	State               string                         `json:"state"`
	Attempts            int                            `json:"attempts"`
	NextAttemptAt       time.Time                      `json:"next_attempt_at"`
	FailureReason       string                         `json:"failure_reason,omitempty"`
	AcknowledgedAt      *time.Time                     `json:"acknowledged_at,omitempty"`
	RefreshAcknowledged bool                           `json:"refresh_acknowledged"`
	AccessAcknowledged  bool                           `json:"access_acknowledged"`
	ClientCiphertext    []byte                         `json:"private_client_ciphertext,omitempty"`
	Config              *MCPCatalogObserverOAuthConfig `json:"private_config,omitempty"`
	Ciphertext          []byte                         `json:"private_ciphertext,omitempty"`
	LeaseID             string                         `json:"private_lease_id,omitempty"`
	LeaseUntil          *time.Time                     `json:"private_lease_until,omitempty"`
}

func CatalogOAuthLifecyclePublic(in *MCPCatalogOAuthLifecycle) *MCPCatalogOAuthLifecycle {
	if in == nil {
		return nil
	}
	out := *in
	out.History = append([]MCPCatalogOAuthEvent(nil), in.History...)
	if in.Revocation != nil {
		job := *in.Revocation
		job.Config = nil
		job.Ciphertext = nil
		job.ClientCiphertext = nil
		job.LeaseID = ""
		job.LeaseUntil = nil
		out.Revocation = &job
	}
	return &out
}
