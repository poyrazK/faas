package api

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/lazyregexp"
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
	// Credential selects what callers present. Empty means a bearer JWT
	// verified against the rule's issuer and JWKS. MCPCredentialConsumerKey
	// means a consumer key (ADR-120) already verified by the consumer-auth
	// gate; the rule's JWT fields are then a fail-closed shape for gateways
	// that predate this field (ADR-20261122194324).
	Credential string `json:"credential,omitempty"`
	// ToolLimits caps tools/call per caller and tool name.
	ToolLimits map[string]MCPToolLimit `json:"tool_limits,omitempty"`
	// Audience is the token audience an external OAuth issuer uses for this
	// server when it is not the resource, such as Entra's "api://…" IDs
	// (ADR-20261123192254). The rule's JWT audience must then be exactly
	// it. Bearer-token policies only.
	Audience string `json:"audience,omitempty"`
}

// MCPCredentialConsumerKey is the MCPResourcePolicy.Credential for api-key
// MCP servers.
const MCPCredentialConsumerKey = "consumer_key"

// An api-key MCP policy still carries a complete JWT shape so a gateway that
// predates MCPResourcePolicy.Credential fails closed: it verifies bearer
// tokens against MCPConsumerKeyJWKSPath, an empty key set apid serves, under
// the issuer MCPConsumerKeyIssuerPath, which signs nothing
// (ADR-20261122194324). Both are paths on the platform API origin.
const (
	MCPConsumerKeyIssuerPath = "/v1/mcp/consumer-key-issuer"
	MCPConsumerKeyJWKSPath   = "/v1/mcp/consumer-key-jwks.json"
)

// MCPGatewayAuthorizedHeader is set by the gateway, to the policy's rule ID,
// on a request an MCP resource policy admitted; the gateway removes any
// client-supplied value first. Servers in gateway-only modes (api-key,
// oauth-proxy) refuse MCP requests without it, so a disabled, expired or
// deleted policy fails closed (ADR-20261122194324).
const MCPGatewayAuthorizedHeader = "Gregale-Mcp-Gateway-Authorized"

// CodeMCPToolRateLimited is the problem code of a tools/call refused by an
// MCP policy tool limit.
const CodeMCPToolRateLimited = "mcp_tool_rate_limited"

// MCP billing labels (ADR-20261123192254). The gateway gives each billable
// MCP request an ordinary "METHOD /template" billing route, so rate-card
// route weights, monthly allowances and plan limits price and cap MCP tools
// like HTTP routes: "MCP /tools/<name>", "MCP /prompts/<name>", the unnamed
// "MCP /tools" and "MCP /prompts" for entries the policy does not list, and
// "MCP /resources". Protocol overhead has no label and no billable units.
const (
	MCPBillingToolsPrefix    = "MCP /tools"
	MCPBillingPromptsPrefix  = "MCP /prompts"
	MCPBillingResourcesLabel = "MCP /resources"
)

// MCPToolBillingLabel is the billing route of a call to tool.
func MCPToolBillingLabel(tool string) string { return MCPBillingToolsPrefix + "/" + tool }

// ValidMCPBillingName reports whether an MCP tool or prompt name can appear
// in a billing label: 1 to MCPBillingNameMaxBytes ASCII letters, digits,
// '_', '-' or '.', the MCP tool-name alphabet.
func ValidMCPBillingName(name string) bool {
	if name == "" || len(name) > MCPBillingNameMaxBytes {
		return false
	}
	for i := 0; i < len(name); i++ {
		switch c := name[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-', c == '.':
		default:
			return false
		}
	}
	return true
}

// MCPToolLimit allows Requests tools/call requests per caller in each
// WindowSeconds window.
type MCPToolLimit struct {
	Requests      int `json:"requests"`
	WindowSeconds int `json:"window_seconds"`
}

func (l MCPToolLimit) validate() error {
	if l.Requests < 1 || l.Requests > MCPToolLimitMaxRequests || l.WindowSeconds < 1 || l.WindowSeconds > MCPToolLimitMaxWindowSeconds {
		return fmt.Errorf("MCP tool limits need 1 to %d requests per 1 to %d seconds", MCPToolLimitMaxRequests, MCPToolLimitMaxWindowSeconds)
	}
	return nil
}

// ConsumerKey reports whether callers present consumer keys.
func (p *MCPResourcePolicy) ConsumerKey() bool {
	return p != nil && p.Credential == MCPCredentialConsumerKey
}

var mcpSimpleVariable = lazyregexp.New(`\{[A-Za-z_][A-Za-z0-9_]*\}`)

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
	if p.Audience != "" && (p.Credential != "" || len(p.Audience) > MCPJWTAudienceMaxBytes || strings.IndexFunc(p.Audience, func(r rune) bool { return r <= 0x20 || r == 0x7f }) >= 0) {
		return fmt.Errorf("MCP audience must be a bearer-token policy's audience of at most %d bytes without spaces", MCPJWTAudienceMaxBytes)
	}
	if p.Credential != "" && p.Credential != MCPCredentialConsumerKey {
		return fmt.Errorf("MCP credential must be empty or %q", MCPCredentialConsumerKey)
	}
	if len(p.ToolLimits) > MCPPolicyMaxEntries {
		return fmt.Errorf("MCP tool limits exceed %d entries", MCPPolicyMaxEntries)
	}
	for name, limit := range p.ToolLimits {
		if name == "" || len(name) > MCPPolicyMaxKeyBytes || strings.IndexFunc(name, func(r rune) bool { return r <= 32 || r == 127 }) >= 0 {
			return fmt.Errorf("MCP tool limits require nonempty tool names")
		}
		if err := limit.validate(); err != nil {
			return err
		}
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
		if re := mcpTemplateMatcher(template); re != nil && re.MatchString(uri) {
			matched = true
			if !MCPHasScopes(required, granted) {
				return false
			}
		}
	}
	return matched
}

// mcpTemplateMatchers caches each resource template's compiled matcher. The
// gateway checks every MCP request against the deployed policy, so the same
// templates repeat; compiling them per request cost a regex compile per
// template per call. The cache is dropped when full, so distinct templates
// cannot grow it without bound.
var mcpTemplateMatchers struct {
	sync.Mutex
	byTemplate map[string]*regexp.Regexp
}

const mcpTemplateMatcherCacheMax = 4096

// mcpTemplateMatcher returns the anchored matcher for template, in which each
// {name} variable matches one path segment. It returns nil when the template
// cannot compile, which matches nothing.
func mcpTemplateMatcher(template string) *regexp.Regexp {
	mcpTemplateMatchers.Lock()
	re, cached := mcpTemplateMatchers.byTemplate[template]
	mcpTemplateMatchers.Unlock()
	if cached {
		return re
	}
	pattern := regexp.QuoteMeta(template)
	for _, variable := range mcpSimpleVariable.FindAllString(template, -1) {
		pattern = strings.ReplaceAll(pattern, regexp.QuoteMeta(variable), `[^/?#]+`)
	}
	re, err := regexp.Compile("^" + pattern + "$")
	if err != nil {
		re = nil
	}
	mcpTemplateMatchers.Lock()
	if mcpTemplateMatchers.byTemplate == nil || len(mcpTemplateMatchers.byTemplate) >= mcpTemplateMatcherCacheMax {
		mcpTemplateMatchers.byTemplate = make(map[string]*regexp.Regexp)
	}
	mcpTemplateMatchers.byTemplate[template] = re
	mcpTemplateMatchers.Unlock()
	return re
}

// ConsumerKeyMCPScopePrefix namespaces consumer-key scopes that only grant
// MCP access (ADR-20261122194324). A key scope "mcp:tools" satisfies the MCP
// policy scope "tools".
const ConsumerKeyMCPScopePrefix = "mcp:"

// ValidConsumerKeyMCPScope reports whether scope is a well-formed "mcp:<scope>".
func ValidConsumerKeyMCPScope(scope string) bool {
	name, ok := strings.CutPrefix(scope, ConsumerKeyMCPScopePrefix)
	if !ok || name == "" || len(scope) > ConsumerKeyMCPScopeMaxBytes {
		return false
	}
	return strings.IndexFunc(name, func(r rune) bool { return r <= 32 || r >= 127 || r == '"' || r == '\\' }) < 0
}

// ConsumerKeyMCPScopes returns the MCP scopes a consumer key grants, without
// the namespace.
func ConsumerKeyMCPScopes(scopes []string) []string {
	var out []string
	for _, scope := range scopes {
		if ValidConsumerKeyMCPScope(scope) {
			out = append(out, strings.TrimPrefix(scope, ConsumerKeyMCPScopePrefix))
		}
	}
	return out
}
