// Package mcphosting defines the source-controlled MCP hosting contract and
// bounded diagnostic client. Workloads still use the ordinary app lifecycle.
package mcphosting

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var endpointPath = regexp.MustCompile(`^/(?:[A-Za-z0-9._~-]+/?)*$`)

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
	Version        int        `json:"version"`
	Endpoint       string     `json:"endpoint"`
	Transport      string     `json:"transport"`
	Mode           string     `json:"mode"`
	Legacy         bool       `json:"legacy"`
	AllowedOrigins []string   `json:"allowed_origins"`
	Auth           AuthConfig `json:"auth"`
}

type AuthConfig struct {
	Mode       string          `json:"mode"`
	Issuer     string          `json:"issuer,omitempty"`
	JWKSURL    string          `json:"jwks_url,omitempty"`
	Resource   string          `json:"resource,omitempty"`
	Scopes     []string        `json:"scopes,omitempty"`
	ToolScopes ToolScopePolicy `json:"tool_scopes,omitzero"`
}

// ToolScopePolicy is optional for existing servers. A configured map denies
// unlisted tools; an explicit empty scope array permits the endpoint's callers.
type ToolScopePolicy map[string][]string

func (p *ToolScopePolicy) UnmarshalJSON(body []byte) error {
	type policy ToolScopePolicy
	var decoded policy
	if err := json.Unmarshal(body, &decoded); err != nil {
		return fmt.Errorf("decode tool_scopes: %w", err)
	}
	if decoded == nil {
		return fmt.Errorf("tool_scopes must be an object; omit it for endpoint-only authorization")
	}
	*p = ToolScopePolicy(decoded)
	return nil
}

func (p ToolScopePolicy) validate(mode string) error {
	for name, scopes := range p {
		if name == "" || strings.IndexFunc(name, func(r rune) bool { return r <= 0x20 || r == 0x7f }) >= 0 {
			return fmt.Errorf("tool_scopes keys must be nonempty tool names without ASCII whitespace or controls")
		}
		if scopes == nil {
			return fmt.Errorf("tool_scopes values must be explicit scope arrays")
		}
		if mode == "open" && len(scopes) > 0 {
			return fmt.Errorf("scoped tools require external-oauth")
		}
		if err := validateScopes(scopes); err != nil {
			return err
		}
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
	if err := c.Auth.ToolScopes.validate(c.Auth.Mode); err != nil {
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
