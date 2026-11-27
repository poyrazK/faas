// Package mcphosting defines the source-controlled MCP hosting contract and
// bounded diagnostic client. Workloads still use the ordinary app lifecycle.
package mcphosting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var uriTemplateExpression = regexp.MustCompile(`\{(?:[+#./;?&])?[A-Za-z0-9_.%~-]+(?:\*|:[1-9][0-9]*)?(?:,(?:[A-Za-z0-9_.%~-]+(?:\*|:[1-9][0-9]*)?))*\}`)
var taskEnvName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func validateEndpoint(value string) error {
	if !api.ValidMCPProbeEndpointPath(value) {
		return fmt.Errorf("MCP endpoint must be a literal absolute path without escapes, query, fragment or route patterns")
	}
	return nil
}

const (
	ConfigFile            = "gregale-mcp.json"
	ProtocolVersion       = api.MCPProtocolVersion
	LegacyProtocolVersion = api.MCPLegacyProtocolVersion
)

// Config is deliberately separate from the guest and deployment manifests:
// it ships with customer source and can run on an existing HTTP app host.
type Config struct {
	Version        int          `json:"version"`
	Endpoint       string       `json:"endpoint"`
	Transport      string       `json:"transport"`
	Mode           string       `json:"mode"`
	Legacy         bool         `json:"legacy"`
	AllowedOrigins []string     `json:"allowed_origins"`
	Auth           AuthConfig   `json:"auth"`
	Tasks          *TasksConfig `json:"tasks,omitempty"`
}

// TasksConfig describes optional starter-owned durable task processing. The
// CLI validates the contract but the starter owns the worker implementation.
type TaskOwnerKeyReference struct {
	ID  string `json:"id"`
	Env string `json:"env"`
}
type TasksConfig struct {
	OwnerKeyID               string                  `json:"owner_key_id,omitempty"`
	PreviousOwnerKeys        []TaskOwnerKeyReference `json:"previous_owner_keys,omitempty"`
	PayloadEncryptionVersion int                     `json:"payload_encryption_version,omitempty"`
	Enabled                  *bool                   `json:"enabled"`
	DatabaseURLEnv           string                  `json:"database_url_env,omitempty"`
	EncryptionKeysEnv        string                  `json:"encryption_keys_env,omitempty"`
	OwnerKeyEnv              string                  `json:"owner_key_env,omitempty"`
	NamespaceEnv             string                  `json:"namespace_env,omitempty"`
	TTLSeconds               int                     `json:"ttl_seconds,omitempty"`
	ShutdownTimeoutMS        int                     `json:"shutdown_timeout_ms,omitempty"`
	PollIntervalMS           int                     `json:"poll_interval_ms,omitempty"`
	MaxAttempts              int                     `json:"max_attempts,omitempty"`
	RetryBaseDelayMS         int                     `json:"retry_base_delay_ms,omitempty"`
	RetryMaxDelayMS          int                     `json:"retry_max_delay_ms,omitempty"`
	WorkerConcurrency        int                     `json:"worker_concurrency,omitempty"`
	MaxRunning               int                     `json:"max_running,omitempty"`
	MaxRunningPerOwner       int                     `json:"max_running_per_owner,omitempty"`
	MaxOutstanding           int                     `json:"max_outstanding,omitempty"`
	MaxOutstandingPerOwner   int                     `json:"max_outstanding_per_owner,omitempty"`
}

type AuthConfig struct {
	Mode     string `json:"mode"`
	Issuer   string `json:"issuer,omitempty"`
	JWKSURL  string `json:"jwks_url,omitempty"`
	Resource string `json:"resource,omitempty"`
	// Audience is the aud claim an external oauth-proxy issuer puts in its
	// access tokens when it cannot use the resource URL; empty means the
	// resource (ADR-20261123192254).
	Audience       string          `json:"audience,omitempty"`
	Scopes         []string        `json:"scopes,omitempty"`
	ToolScopes     ToolScopePolicy `json:"tool_scopes,omitzero"`
	ResourceScopes ScopePolicy     `json:"resource_scopes,omitzero"`
	PromptScopes   ScopePolicy     `json:"prompt_scopes,omitzero"`
	// ToolLimits caps tools/call per caller (ADR-20261122194324). Callers
	// are identified by key or token, so open servers cannot use them.
	ToolLimits map[string]api.MCPToolLimit `json:"tool_limits,omitempty"`
}

// Auth modes. AuthAPIKey and AuthOAuthProxy are enforced only at the gateway
// (ADR-20261122194324).
const (
	AuthOpen          = "open"
	AuthExternalOAuth = "external-oauth"
	AuthAPIKey        = "api-key"
	AuthOAuthProxy    = "oauth-proxy"
)

// GatewayOnly reports whether the mode has no guest-side verification: the
// gateway policy is the only access control.
func (a AuthConfig) GatewayOnly() bool { return a.Mode == AuthAPIKey || a.Mode == AuthOAuthProxy }

// ExternalIssuer reports an oauth-proxy server whose tokens come from an
// owner-chosen OpenID Connect issuer instead of Gregale Auth: its jwks_url
// was discovered from the issuer's metadata (ADR-20261123192254).
func (a AuthConfig) ExternalIssuer() bool { return a.Mode == AuthOAuthProxy && a.JWKSURL != "" }

// TokenAudience is the aud claim the gateway requires.
func (a AuthConfig) TokenAudience() string {
	if a.Audience != "" {
		return a.Audience
	}
	return a.Resource
}

// UsesJWT reports whether callers present bearer JWTs from an issuer.
func (a AuthConfig) UsesJWT() bool { return a.Mode == AuthExternalOAuth || a.Mode == AuthOAuthProxy }

// ToolScopePolicy preserves the existing tool policy type name.
type ToolScopePolicy = ScopePolicy

// ScopePolicy is an optional per-catalog-entry allowlist. Its keys are names,
// except resource policies, whose keys are resource URIs or URI templates.
type ScopePolicy map[string][]string

func (p *ScopePolicy) UnmarshalJSON(body []byte) error {
	type policy ScopePolicy
	var decoded policy
	if err := json.Unmarshal(body, &decoded); err != nil {
		return fmt.Errorf("decode scope policy: %w", err)
	}
	if decoded == nil {
		return fmt.Errorf("scope policy must be an object; omit it for endpoint-only authorization")
	}
	*p = ScopePolicy(decoded)
	return nil
}

func (p ScopePolicy) validate(field, mode string, validateKey func(string) error) error {
	for name, scopes := range p {
		if err := validateKey(name); err != nil {
			return fmt.Errorf("%s key %q is invalid: %w", field, name, err)
		}
		if scopes == nil {
			return fmt.Errorf("%s values must be explicit scope arrays", field)
		}
		if (mode == AuthOpen || mode == AuthOAuthProxy) && len(scopes) > 0 {
			return fmt.Errorf("scoped %s entries require external-oauth or api-key", field)
		}
		if err := validateScopes(scopes); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	return nil
}

func validatePolicyName(name string) error {
	if name == "" || strings.IndexFunc(name, func(r rune) bool { return r <= 0x20 || r == 0x7f }) >= 0 {
		return fmt.Errorf("expected a nonempty name without ASCII whitespace or controls")
	}
	return nil
}

func validateResourcePolicyKey(value string) error {
	if value == "" || len(value) > 4096 || strings.IndexFunc(value, func(r rune) bool { return r <= 0x20 || r == 0x7f }) >= 0 {
		return fmt.Errorf("expected an absolute URI or URI template without whitespace or controls")
	}
	expanded := uriTemplateExpression.ReplaceAllString(value, "mcp-template-value")
	if strings.ContainsAny(expanded, "{}") {
		return fmt.Errorf("expected a valid URI template expression")
	}
	u, err := url.Parse(expanded)
	if err != nil || u.Scheme == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("expected an absolute URI or URI template without credentials or fragment")
	}
	return nil
}

func Load(dir string) (Config, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Config{}, fmt.Errorf("open MCP source directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(ConfigFile)
	if err != nil {
		return Config{}, fmt.Errorf("stat %s: %w", ConfigFile, err)
	}
	if !info.Mode().IsRegular() {
		return Config{}, fmt.Errorf("%s must be a regular file in the source directory", ConfigFile)
	}
	f, err := root.Open(ConfigFile)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", ConfigFile, err)
	}
	defer func() { _ = f.Close() }()
	return Decode(f)
}

func Decode(r io.Reader) (Config, error) {
	var c Config
	body, err := io.ReadAll(io.LimitReader(r, (64<<10)+1))
	if err != nil {
		return c, fmt.Errorf("read MCP config: %w", err)
	}
	if len(body) > 64<<10 {
		return c, fmt.Errorf("MCP config exceeds 64 KiB")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err == nil {
		if tasks, ok := fields["tasks"]; ok && bytes.Equal(bytes.TrimSpace(tasks), []byte("null")) {
			return c, fmt.Errorf("tasks must be an object when present")
		}
	}
	d := json.NewDecoder(strings.NewReader(string(body)))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, fmt.Errorf("decode MCP config: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return c, fmt.Errorf("MCP config must contain one JSON object")
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("MCP config version must be 1")
	}
	if c.Transport != "streamable-http" {
		return fmt.Errorf("MCP transport must be streamable-http; stdio requires an HTTP adapter")
	}
	if c.Mode != "stateless" {
		return fmt.Errorf("MCP mode must be stateless; instance-local sessions cannot survive park or scale-out")
	}
	if c.AllowedOrigins == nil {
		return fmt.Errorf("allowed_origins must be an explicit array; [] rejects browser origins")
	}
	if err := validateEndpoint(c.Endpoint); err != nil {
		return err
	}
	for _, origin := range c.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("allowed_origins must contain exact HTTP origins")
		}
	}
	// An external oauth-proxy issuer can issue any scope, so its catalog
	// entries may be scoped like external-oauth ones.
	scopeMode := c.Auth.Mode
	if c.Auth.ExternalIssuer() {
		scopeMode = AuthExternalOAuth
	}
	if err := c.Auth.ToolScopes.validate("tool_scopes", scopeMode, validatePolicyName); err != nil {
		return err
	}
	if err := c.Auth.ResourceScopes.validate("resource_scopes", scopeMode, validateResourcePolicyKey); err != nil {
		return err
	}
	if err := c.Auth.PromptScopes.validate("prompt_scopes", scopeMode, validatePolicyName); err != nil {
		return err
	}
	if err := c.Tasks.validate(); err != nil {
		return err
	}
	if c.Auth.GatewayOnly() && c.Tasks != nil && c.Tasks.Enabled != nil && *c.Tasks.Enabled {
		// The server cannot tell callers apart in these modes, so tasks would
		// have no owner (ADR-20261122194324).
		return fmt.Errorf("tasks need per-caller ownership; use external-oauth with tasks enabled")
	}
	return c.Auth.validate(c.Endpoint)
}

// maxAudienceBytes bounds auth.audience.
const maxAudienceBytes = api.MCPJWTAudienceMaxBytes

// OAuthProxyScopes are the scopes a Gregale Auth OAuth server issues.
var OAuthProxyScopes = map[string]bool{"openid": true, "email": true, "profile": true, "phone": true}

// OAuthProxyJWKSURL is the key set of a Gregale Auth issuer
// (https://<auth app>/auth/v1).
func OAuthProxyJWKSURL(issuer string) string {
	return strings.TrimSuffix(issuer, "/") + "/.well-known/jwks.json"
}

func (a AuthConfig) validate(endpoint string) error {
	if len(a.ToolLimits) > api.MCPPolicyMaxEntries {
		return fmt.Errorf("auth.tool_limits exceeds %d entries", api.MCPPolicyMaxEntries)
	}
	for name, limit := range a.ToolLimits {
		if validatePolicyName(name) != nil {
			return fmt.Errorf("auth.tool_limits keys must be tool names")
		}
		if limit.Requests < 1 || limit.Requests > api.MCPToolLimitMaxRequests || limit.WindowSeconds < 1 || limit.WindowSeconds > api.MCPToolLimitMaxWindowSeconds {
			return fmt.Errorf("auth.tool_limits.%s needs 1 to %d requests per 1 to %d seconds", name, api.MCPToolLimitMaxRequests, api.MCPToolLimitMaxWindowSeconds)
		}
	}
	switch a.Mode {
	case AuthOpen:
		if a.Issuer != "" || a.JWKSURL != "" || a.Resource != "" || a.Audience != "" || len(a.Scopes) > 0 || len(a.ToolLimits) > 0 {
			return fmt.Errorf("open auth must not contain OAuth settings or tool limits")
		}
		return nil
	case AuthExternalOAuth, AuthAPIKey, AuthOAuthProxy:
	default:
		return fmt.Errorf("auth.mode must explicitly be open, external-oauth, api-key or oauth-proxy")
	}
	urls := []struct{ name, value string }{{"resource", a.Resource}}
	switch a.Mode {
	case AuthExternalOAuth:
		urls = append(urls, struct{ name, value string }{"issuer", a.Issuer}, struct{ name, value string }{"jwks_url", a.JWKSURL})
	case AuthOAuthProxy:
		urls = append(urls, struct{ name, value string }{"issuer", a.Issuer})
		if a.JWKSURL != "" {
			urls = append(urls, struct{ name, value string }{"jwks_url", a.JWKSURL})
		}
	case AuthAPIKey:
		if a.Issuer != "" || a.JWKSURL != "" {
			return fmt.Errorf("api-key auth must not name an issuer or jwks_url")
		}
	}
	for _, field := range urls {
		u, err := url.Parse(field.value)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("auth.%s must be an HTTPS URL without credentials, query, or fragment", field.name)
		}
	}
	resource, _ := url.Parse(a.Resource)
	if resource.Path != endpoint {
		return fmt.Errorf("auth.resource must identify the canonical MCP endpoint")
	}
	if a.Audience != "" && (!a.ExternalIssuer() || len(a.Audience) > maxAudienceBytes || strings.IndexFunc(a.Audience, func(r rune) bool { return r <= 0x20 || r == 0x7f }) >= 0) {
		return fmt.Errorf("auth.audience is only for an external oauth-proxy issuer and must be 1 to %d bytes without whitespace", maxAudienceBytes)
	}
	switch {
	case a.ExternalIssuer():
		if len(a.Scopes) == 0 {
			return fmt.Errorf("oauth-proxy with an external issuer requires at least one scope")
		}
	case a.Mode == AuthOAuthProxy:
		// Gregale Auth client tokens are audience-bound to the resource and
		// carry only OpenID scopes, so per-entry scopes cannot gate tools.
		if issuer, _ := url.Parse(a.Issuer); issuer.Path != "/auth/v1" {
			return fmt.Errorf("oauth-proxy auth.issuer must be a Gregale Auth issuer, https://<auth app>/auth/v1, or an OpenID Connect issuer with its jwks_url")
		}
		if len(a.Scopes) == 0 {
			return fmt.Errorf("oauth-proxy requires at least one scope, such as openid")
		}
		for _, scope := range a.Scopes {
			if !OAuthProxyScopes[scope] {
				return fmt.Errorf("oauth-proxy scope %q: Gregale Auth issues only the openid, email, profile and phone scopes", scope)
			}
		}
		return nil
	case a.Mode == AuthAPIKey:
		if len(a.Scopes) == 0 {
			return fmt.Errorf("api-key requires at least one scope; consumer keys grant it as mcp:<scope>")
		}
	default:
		if len(a.Scopes) == 0 {
			return fmt.Errorf("external-oauth requires at least one scope")
		}
	}
	return validateScopes(a.Scopes)
}

func (c *TasksConfig) validate() error {
	if c != nil {
		if c.OwnerKeyID != "" && !regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`).MatchString(c.OwnerKeyID) {
			return fmt.Errorf("invalid tasks.owner_key_id")
		}
		if len(c.PreviousOwnerKeys) > 16 || c.PayloadEncryptionVersion < 0 || c.PayloadEncryptionVersion > 2 {
			return fmt.Errorf("invalid task encryption settings")
		}
		seen := map[string]bool{}
		if c.OwnerKeyID != "" {
			seen[c.OwnerKeyID] = true
		}
		for _, key := range c.PreviousOwnerKeys {
			if !regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`).MatchString(key.ID) || !regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`).MatchString(key.Env) || seen[key.ID] {
				return fmt.Errorf("invalid tasks.previous_owner_keys")
			}
			seen[key.ID] = true
		}
	}

	if c == nil {
		return nil
	}
	if c.Enabled == nil {
		return fmt.Errorf("tasks.enabled must be explicitly true or false")
	}
	if !*c.Enabled {
		return nil
	}
	for _, field := range []struct{ name, value string }{
		{"database_url_env", c.DatabaseURLEnv},
		{"owner_key_env", c.OwnerKeyEnv},
		{"namespace_env", c.NamespaceEnv},
	} {
		if !taskEnvName.MatchString(field.value) {
			return fmt.Errorf("tasks.%s must name an uppercase environment variable", field.name)
		}
	}
	if c.EncryptionKeysEnv != "" {
		if !taskEnvName.MatchString(c.EncryptionKeysEnv) {
			return fmt.Errorf("tasks.encryption_keys_env must name an uppercase environment variable")
		}
		if c.EncryptionKeysEnv == c.DatabaseURLEnv || c.EncryptionKeysEnv == c.OwnerKeyEnv || c.EncryptionKeysEnv == c.NamespaceEnv {
			return fmt.Errorf("tasks encryption keys must use a distinct environment variable")
		}
	}
	if c.DatabaseURLEnv == c.OwnerKeyEnv || c.DatabaseURLEnv == c.NamespaceEnv || c.OwnerKeyEnv == c.NamespaceEnv {
		return fmt.Errorf("tasks database, owner key, and namespace must use distinct environment variables")
	}
	if c.TTLSeconds != 0 && (c.TTLSeconds < 60 || c.TTLSeconds > 30*24*60*60) {
		return fmt.Errorf("tasks.ttl_seconds must be between 60 seconds and 30 days")
	}
	if c.PollIntervalMS != 0 && (c.PollIntervalMS < 500 || c.PollIntervalMS > 30_000) {
		return fmt.Errorf("tasks.poll_interval_ms must be between 500 and 30000")
	}
	if c.ShutdownTimeoutMS != 0 && (c.ShutdownTimeoutMS < 1000 || c.ShutdownTimeoutMS > 300_000) {
		return fmt.Errorf("tasks.shutdown_timeout_ms must be between 1000 and 300000")
	}
	if c.WorkerConcurrency != 0 && (c.WorkerConcurrency < 1 || c.WorkerConcurrency > 16) {
		return fmt.Errorf("tasks.worker_concurrency must be between 1 and 16")
	}
	if c.MaxAttempts < 0 || c.MaxAttempts > 10 {
		return fmt.Errorf("tasks.max_attempts must be between 1 and 10 when specified")
	}
	base, maximum := c.RetryBaseDelayMS, c.RetryMaxDelayMS
	if base == 0 {
		base = 1000
	}
	if maximum == 0 {
		maximum = 60000
	}
	if base < 100 || base > 86400000 || maximum < base || maximum > 86400000 {
		return fmt.Errorf("task retry delays must be between 100 and 86400000 milliseconds, with maximum at least base")
	}
	if c.MaxRunning < 0 || c.MaxRunningPerOwner < 0 {
		return fmt.Errorf("task running limits must be positive integers when specified")
	}
	running, runningOwner := c.MaxRunning, c.MaxRunningPerOwner
	if running == 0 {
		running = api.MCPTaskDefaultMaxRunning
	}
	if runningOwner == 0 {
		runningOwner = api.MCPTaskDefaultMaxRunningPerOwner
	}
	if runningOwner > running {
		return fmt.Errorf("tasks.max_running_per_owner must not exceed tasks.max_running")
	}
	if c.MaxOutstanding < 0 || c.MaxOutstandingPerOwner < 0 {
		return fmt.Errorf("task admission limits must be positive integers when specified")
	}
	total, owner := c.MaxOutstanding, c.MaxOutstandingPerOwner
	if total == 0 {
		total = api.MCPTaskDefaultMaxOutstanding
	}
	if owner == 0 {
		owner = api.MCPTaskDefaultMaxOutstandingPerOwner
	}
	if owner > total {
		return fmt.Errorf("tasks.max_outstanding_per_owner must not exceed tasks.max_outstanding")
	}
	return nil
}

func validateScopes(scopes []string) error {
	for _, scope := range scopes {
		if scope == "" || strings.ContainsAny(scope, " \t\r\n\"\\") {
			return fmt.Errorf("OAuth scopes must be nonempty tokens")
		}
		for _, r := range scope {
			if r < 0x21 || r > 0x7e {
				return fmt.Errorf("OAuth scopes must be ASCII tokens")
			}
		}
	}
	return nil
}

func (c Config) URL(base string) (string, error) {
	if err := validateEndpoint(c.Endpoint); err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("base URL must be HTTP(S) without credentials, query, or fragment")
	}
	u.Path, u.RawPath = c.Endpoint, ""
	return u.String(), nil
}
