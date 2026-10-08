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

var endpointPath = regexp.MustCompile(`^/(?:[A-Za-z0-9._~-]+/?)*$`)
var uriTemplateExpression = regexp.MustCompile(`\{(?:[+#./;?&])?[A-Za-z0-9_.%~-]+(?:\*|:[1-9][0-9]*)?(?:,(?:[A-Za-z0-9_.%~-]+(?:\*|:[1-9][0-9]*)?))*\}`)
var taskEnvName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func validateEndpoint(value string) error {
	if !endpointPath.MatchString(value) || len(value) > 1024 {
		return fmt.Errorf("MCP endpoint must be a literal absolute path without escapes, query, fragment or route patterns")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("MCP endpoint must not contain dot segments")
		}
	}
	return nil
}

const (
	ConfigFile            = "gregale-mcp.json"
	ProtocolVersion       = "2026-07-28"
	LegacyProtocolVersion = "2025-11-25"
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
type TasksConfig struct {
	Enabled                *bool  `json:"enabled"`
	DatabaseURLEnv         string `json:"database_url_env,omitempty"`
	EncryptionKeysEnv      string `json:"encryption_keys_env,omitempty"`
	OwnerKeyEnv            string `json:"owner_key_env,omitempty"`
	NamespaceEnv           string `json:"namespace_env,omitempty"`
	TTLSeconds             int    `json:"ttl_seconds,omitempty"`
	ShutdownTimeoutMS      int    `json:"shutdown_timeout_ms,omitempty"`
	PollIntervalMS         int    `json:"poll_interval_ms,omitempty"`
	MaxAttempts            int    `json:"max_attempts,omitempty"`
	RetryBaseDelayMS       int    `json:"retry_base_delay_ms,omitempty"`
	RetryMaxDelayMS        int    `json:"retry_max_delay_ms,omitempty"`
	WorkerConcurrency      int    `json:"worker_concurrency,omitempty"`
	MaxRunning             int    `json:"max_running,omitempty"`
	MaxRunningPerOwner     int    `json:"max_running_per_owner,omitempty"`
	MaxOutstanding         int    `json:"max_outstanding,omitempty"`
	MaxOutstandingPerOwner int    `json:"max_outstanding_per_owner,omitempty"`
}

type AuthConfig struct {
	Mode           string          `json:"mode"`
	Issuer         string          `json:"issuer,omitempty"`
	JWKSURL        string          `json:"jwks_url,omitempty"`
	Resource       string          `json:"resource,omitempty"`
	Scopes         []string        `json:"scopes,omitempty"`
	ToolScopes     ToolScopePolicy `json:"tool_scopes,omitzero"`
	ResourceScopes ScopePolicy     `json:"resource_scopes,omitzero"`
	PromptScopes   ScopePolicy     `json:"prompt_scopes,omitzero"`
}

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
		if mode == "open" && len(scopes) > 0 {
			return fmt.Errorf("scoped %s entries require external-oauth", field)
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
	if err := c.Auth.ToolScopes.validate("tool_scopes", c.Auth.Mode, validatePolicyName); err != nil {
		return err
	}
	if err := c.Auth.ResourceScopes.validate("resource_scopes", c.Auth.Mode, validateResourcePolicyKey); err != nil {
		return err
	}
	if err := c.Auth.PromptScopes.validate("prompt_scopes", c.Auth.Mode, validatePolicyName); err != nil {
		return err
	}
	if err := c.Tasks.validate(); err != nil {
		return err
	}
	if c.Auth.Mode == "open" {
		if c.Auth.Issuer != "" || c.Auth.JWKSURL != "" || c.Auth.Resource != "" || len(c.Auth.Scopes) > 0 {
			return fmt.Errorf("open auth must not contain OAuth settings")
		}
		return nil
	}
	if c.Auth.Mode != "external-oauth" {
		return fmt.Errorf("auth.mode must explicitly be open or external-oauth")
	}
	for _, field := range []struct{ name, value string }{{"issuer", c.Auth.Issuer}, {"jwks_url", c.Auth.JWKSURL}, {"resource", c.Auth.Resource}} {
		value := field.value
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("auth.%s must be an HTTPS URL without credentials, query, or fragment", field.name)
		}
	}
	resource, _ := url.Parse(c.Auth.Resource)
	if resource.Path != c.Endpoint {
		return fmt.Errorf("auth.resource must identify the canonical MCP endpoint")
	}
	if len(c.Auth.Scopes) == 0 {
		return fmt.Errorf("external-oauth requires at least one scope")
	}
	return validateScopes(c.Auth.Scopes)
}

func (c *TasksConfig) validate() error {
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
