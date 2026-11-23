package gateway

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// mcpCall is the JSON-RPC method and entry name of an MCP request an MCP
// resource policy admitted. A zero value is a request without a JSON-RPC
// body: a GET stream, a DELETE that ends a session, or a preflight.
type mcpCall struct {
	method string
	name   string
}

// tool is the tool name of a tools/call, for tool limits.
func (c mcpCall) tool() string {
	if c.method == "tools/call" {
		return c.name
	}
	return ""
}

// mcpBillingLabel is the billing route of an admitted MCP request
// (ADR-20261123192254). Tool calls, prompt gets and resource reads are the
// billable work of an MCP server and get a "METHOD /template" label rate
// cards can weight and plans can cap: "MCP /tools/<name>" or
// "MCP /prompts/<name>" for a name the policy lists, else "MCP /tools" or
// "MCP /prompts", and "MCP /resources" for every read. Every other request
// is protocol overhead (initialize, list calls, pings, notifications,
// completions, subscriptions, streams) and returns "".
func mcpBillingLabel(p *api.MCPResourcePolicy, call mcpCall) string {
	switch call.method {
	case "tools/call":
		return mcpNamedBillingLabel(api.MCPBillingToolsPrefix, call.name, p.ToolScopes, p.ToolLimits)
	case "prompts/get":
		return mcpNamedBillingLabel(api.MCPBillingPromptsPrefix, call.name, p.PromptScopes, nil)
	case "resources/read":
		return api.MCPBillingResourcesLabel
	}
	return ""
}

// mcpNamedBillingLabel names an entry only when the policy lists it, so a
// caller cannot spend the app's bounded billing labels on made-up names.
func mcpNamedBillingLabel(prefix, name string, scopes map[string][]string, limits map[string]api.MCPToolLimit) string {
	_, scoped := scopes[name]
	_, limited := limits[name]
	if (scoped || limited) && api.ValidMCPBillingName(name) {
		return prefix + "/" + name
	}
	return prefix
}

type mcpProtocolOverheadKey struct{}

// applyMCPBilling relabels an admitted MCP request for billing. A billable
// call replaces the request's route label, or the candidate a JWT tenant
// rule admits later, with its MCP label. Protocol overhead is marked so it
// carries no billable units and no monthly plan units; it still counts as a
// request and toward per-minute plan limits.
func (h *Handler) applyMCPBilling(r *http.Request, app App, p *api.MCPResourcePolicy, call mcpCall) {
	label := mcpBillingLabel(p, call)
	if label == "" {
		*r = *r.WithContext(context.WithValue(r.Context(), mcpProtocolOverheadKey{}, true))
		return
	}
	if current, _ := r.Context().Value(billingRouteKey{}).(string); current != "" {
		*r = *withBillingRoute(r, h.billingRouteSetFor(app.ID).admit(label))
		return
	}
	if candidate, _ := r.Context().Value(billingRouteCandidateKey{}).(string); candidate != "" {
		*r = *withBillingRouteCandidate(r, label)
	}
}

// mcpProtocolOverheadFrom reports whether r is MCP protocol overhead an MCP
// resource policy admitted (applyMCPBilling).
func mcpProtocolOverheadFrom(r *http.Request) bool {
	if r == nil {
		return false
	}
	overhead, _ := r.Context().Value(mcpProtocolOverheadKey{}).(bool)
	return overhead
}
