package api

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// MCPResourcePolicy adds resource-server behavior to an existing JWT edge rule.
// Scope maps are execution allowlists. Catalog filtering and task ownership
// remain application responsibilities; the gateway never trusts MCP hint headers.
type MCPResourcePolicy struct {
	Resource       string              `json:"resource"`
	Scopes         []string            `json:"scopes,omitempty"`
	AllowedOrigins []string            `json:"allowed_origins,omitempty"`
	ToolScopes     map[string][]string `json:"tool_scopes"`
	ResourceScopes map[string][]string `json:"resource_scopes"`
	PromptScopes   map[string][]string `json:"prompt_scopes"`
}

var mcpSimpleVariable = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_]*\}`)

func (p *MCPResourcePolicy) Validate() error {
	if p == nil {
		return nil
	}
	u, err := url.Parse(p.Resource)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" || u.RawPath != "" || strings.ContainsAny(u.Path, "*{}") {
		return fmt.Errorf("MCP resource must be a canonical HTTPS endpoint")
	}
	for _, origin := range p.AllowedOrigins {
		o, err := url.Parse(origin)
		if err != nil || (o.Scheme != "https" && o.Scheme != "http") || o.Host == "" || o.User != nil || o.Path != "" || o.RawQuery != "" || o.Fragment != "" {
			return fmt.Errorf("MCP allowed origins must be exact HTTP origins")
		}
	}
	if len(p.AllowedOrigins) > MCPPolicyMaxEntries {
		return fmt.Errorf("too many MCP allowed origins")
	}
	if err := validateMCPScopes(p.Scopes); err != nil {
		return err
	}
	for _, policy := range []map[string][]string{p.ToolScopes, p.ResourceScopes, p.PromptScopes} {
		if len(policy) > MCPPolicyMaxEntries {
			return fmt.Errorf("MCP scope policy exceeds %d entries", MCPPolicyMaxEntries)
		}
		for key, scopes := range policy {
			if key == "" || len(key) > MCPPolicyMaxKeyBytes || strings.IndexFunc(key, func(r rune) bool { return r <= 32 || r == 127 }) >= 0 || scopes == nil {
				return fmt.Errorf("MCP scope entries require nonempty keys and explicit scope arrays")
			}
			if err := validateMCPScopes(scopes); err != nil {
				return err
			}
		}
	}
	for key := range p.ResourceScopes {
		expanded := mcpSimpleVariable.ReplaceAllString(key, "value")
		u, err := url.Parse(expanded)
		if err != nil || u.Scheme == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(expanded, "{}") {
			return fmt.Errorf("gateway MCP resource policies support absolute URIs and simple {variable} templates")
		}
	}
	return nil
}

func validateMCPScopes(scopes []string) error {
	if len(scopes) > MCPPolicyMaxEntries {
		return fmt.Errorf("too many MCP scopes")
	}
	for _, scope := range scopes {
		if scope == "" || len(scope) > MCPPolicyMaxKeyBytes || strings.IndexFunc(scope, func(r rune) bool { return r <= 32 || r >= 127 || r == '"' || r == '\\' }) >= 0 {
			return fmt.Errorf("invalid MCP scope")
		}
	}
	return nil
}

func (p *MCPResourcePolicy) EndpointPath() string { u, _ := url.Parse(p.Resource); return u.Path }
func (p *MCPResourcePolicy) MetadataPath() string {
	return "/.well-known/oauth-protected-resource" + p.EndpointPath()
}
func (p *MCPResourcePolicy) MetadataURL() string {
	u, _ := url.Parse(p.Resource)
	u.Path = p.MetadataPath()
	return u.String()
}

func MCPHasScopes(required, granted []string) bool {
	for _, scope := range required {
		if !slices.Contains(granted, scope) {
			return false
		}
	}
	return true
}

func MCPEntryAllowed(policy map[string][]string, name string, granted []string) bool {
	if policy == nil {
		return true
	}
	required, exists := policy[name]
	return exists && MCPHasScopes(required, granted)
}

func MCPResourceAllowed(policy map[string][]string, uri string, granted []string) bool {
	if policy == nil {
		return true
	}
	// Every matching policy must hold, so overlapping templates cannot weaken
	// a literal URI's stronger scope requirement.
	matched := false
	for template, required := range policy {
		pattern := regexp.QuoteMeta(template)
		for _, variable := range mcpSimpleVariable.FindAllString(template, -1) {
			pattern = strings.ReplaceAll(pattern, regexp.QuoteMeta(variable), `[^/?#]+`)
		}
		if ok, _ := regexp.MatchString("^"+pattern+"$", uri); ok {
			matched = true
			if !MCPHasScopes(required, granted) {
				return false
			}
		}
	}
	return matched
}
