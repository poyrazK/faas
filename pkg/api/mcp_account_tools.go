package api

import "time"

// MCP account tools (ADR-20261125194510): with the owner's opt-in, the
// gateway adds these tools to an MCP server's tool list for callers it
// enrolled as consumers through OAuth, and answers their calls itself from
// apid. The names are reserved on such a server.
const (
	MCPAccountToolUsage = "account_usage"
	MCPAccountToolPlans = "account_plans"
)

// MCPAccountTool reports whether name is a gateway account tool.
func MCPAccountTool(name string) bool {
	return name == MCPAccountToolUsage || name == MCPAccountToolPlans
}

// MCPToolDefinition is one entry of an MCP tools/list result.
type MCPToolDefinition struct {
	Name        string             `json:"name"`
	Title       string             `json:"title,omitempty"`
	Description string             `json:"description"`
	InputSchema map[string]any     `json:"inputSchema"`
	Annotations *MCPToolAnnotation `json:"annotations,omitempty"`
}

// MCPToolAnnotation carries the MCP tool hints the account tools set.
type MCPToolAnnotation struct {
	ReadOnlyHint  bool `json:"readOnlyHint"`
	OpenWorldHint bool `json:"openWorldHint"`
}

// MCPAccountToolDefinitions are the tools the gateway appends to tools/list.
func MCPAccountToolDefinitions() []MCPToolDefinition {
	noArgs := func() map[string]any {
		return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	}
	hints := &MCPToolAnnotation{ReadOnlyHint: true}
	return []MCPToolDefinition{
		{
			Name:  MCPAccountToolUsage,
			Title: "Account usage",
			Description: "Your usage of this MCP server this month: your plan and its limits, requests and units used, " +
				"units left, and usage per tool. Usage trails live calls by about a minute.",
			InputSchema: noArgs(), Annotations: hints,
		},
		{
			Name:  MCPAccountToolPlans,
			Title: "Account plans",
			Description: "The plans this MCP server offers with their prices, the plan you are on, and a link to " +
				"change plans when the server allows it. A person opens the link to save a card; this tool never charges.",
			InputSchema: noArgs(), Annotations: hints,
		},
	}
}

// MCPAccountUsage is account_usage's answer.
type MCPAccountUsage struct {
	Plan        *MCPAccountPlanLimits `json:"plan,omitempty"`
	PeriodStart time.Time             `json:"period_start"`
	Until       time.Time             `json:"until"`
	Requests    int64                 `json:"requests"`
	Units       int64                 `json:"units"`
	// UnitsLeft is set when the plan caps units per month.
	UnitsLeft *int64                `json:"units_left,omitempty"`
	Tools     []MCPAccountToolUnits `json:"tools"`
}

// MCPAccountPlanLimits is the caller's plan and its limits.
type MCPAccountPlanLimits struct {
	Name                 string `json:"name"`
	MaxRequestsPerMinute int64  `json:"max_requests_per_minute,omitempty"`
	MaxUnitsPerMonth     int64  `json:"max_units_per_month,omitempty"`
	// ToolLimits are the plan's per-tool caps, by tool name.
	ToolLimits map[string]APIConsumerPlanRouteLimit `json:"tool_limits,omitempty"`
}

// MCPAccountToolUnits is one tool's billable units this period.
type MCPAccountToolUnits struct {
	Tool  string `json:"tool"`
	Units int64  `json:"units"`
}

// MCPAccountPlans is account_plans' answer.
type MCPAccountPlans struct {
	CurrentPlan string               `json:"current_plan,omitempty"`
	Plans       []ConsumerSignupPlan `json:"plans"`
	// UpgradeURL opens the hosted page that changes the caller's plan; it
	// is empty when the owner does not let customers change plans.
	UpgradeURL          string     `json:"upgrade_url,omitempty"`
	UpgradeURLExpiresAt *time.Time `json:"upgrade_url_expires_at,omitempty"`
}
