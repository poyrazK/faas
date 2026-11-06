// adr: 20261105133521
package mcpmanagement

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const agentKeyToolsetsHeader = "Gregale-MCP-Toolsets"

type agentKeyToolsetsContextKey struct{}

// The HTTP bridge is stateless: every request carries its selected catalog.
// All selections for a key use the same management instance and plan journal.
func (m *management) toolListedForContext(ctx context.Context, name string) bool {
	if !m.toolListed(name) {
		return false
	}
	if !m.opts.agentKeyToolsetSelection {
		return true
	}
	selected, ok := ctx.Value(agentKeyToolsetsContextKey{}).([]string)
	if !ok {
		return false
	}
	return hasAllToolsets(selected) || slices.ContainsFunc(selected, func(set string) bool { return toolsetIncludes(name, set) })
}

func (m *management) selectedRecovery(ctx context.Context, recovery Recovery) Recovery {
	if recovery.Tool == "" || m.toolListedForContext(ctx, recovery.Tool) {
		return recovery
	}
	set, known := toolsetOf[recovery.Tool]
	if !known {
		return recovery
	}
	if !slices.Contains(recovery.Toolsets, set) {
		recovery.Toolsets = append(recovery.Toolsets, set)
	}
	recovery.Tool, recovery.Arguments = "", nil
	return recovery
}

func (m *management) selectedToolError(ctx context.Context, name string, args any, err error) *mcp.CallToolResult {
	result := m.toolErrorForCall(name, args, err)
	if !m.opts.agentKeyToolsetSelection {
		return result
	}
	problem := result.StructuredContent.(ToolError)
	problem.Recovery = m.selectedRecovery(ctx, problem.Recovery)
	return errorResult(problem)
}

// Narrow only MCP-owned recovery metadata, never arbitrary native payloads.
// Raw messages preserve exact integer evidence in the rest of each response.
func (m *management) selectedRecoveryOutput(ctx context.Context, data json.RawMessage) (json.RawMessage, error) {
	if !m.opts.agentKeyToolsetSelection {
		return data, nil
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil || root == nil {
		return data, nil //nolint:nilerr // Non-object results have no MCP recovery metadata to filter.
	}
	var visit func(map[string]json.RawMessage)
	visit = func(fields map[string]json.RawMessage) {
		if raw := fields["recovery"]; len(raw) != 0 {
			var r Recovery
			if json.Unmarshal(raw, &r) == nil && r.Action != "" {
				fields["recovery"], _ = json.Marshal(m.selectedRecovery(ctx, r))
			}
		}
		if raw := fields["retry_tool"]; len(raw) != 0 {
			var tool string
			if json.Unmarshal(raw, &tool) == nil {
				if _, known := toolsetOf[tool]; known && !m.toolListedForContext(ctx, tool) {
					delete(fields, "retry_tool")
					fields["next_step"] = json.RawMessage(`"inspect_native_state_before_replanning"`)
				}
			}
		}
		for _, key := range []string{"problem", "action", "verification"} {
			var child map[string]json.RawMessage
			if json.Unmarshal(fields[key], &child) == nil && child != nil {
				visit(child)
				if key == "verification" {
					var next Recovery
					if json.Unmarshal(child["next"], &next) == nil && next.Action != "" {
						child["next"], _ = json.Marshal(m.selectedRecovery(ctx, next))
					}
				}
				fields[key], _ = json.Marshal(child)
			}
		}
		var items []json.RawMessage
		if json.Unmarshal(fields["items"], &items) == nil && items != nil {
			for i, raw := range items {
				var child map[string]json.RawMessage
				if json.Unmarshal(raw, &child) == nil && child != nil {
					visit(child)
					items[i], _ = json.Marshal(child)
				}
			}
			fields["items"], _ = json.Marshal(items)
		}
	}
	visit(root)
	out, err := json.Marshal(root)
	if err != nil || len(out) > maxResultBytes {
		return nil, invalid("response_too_large", "selected catalog recovery exceeds the MCP output bound")
	}
	return out, nil
}

func registerAgentKeyCatalog(s *mcp.Server, m *management) {
	if !m.opts.agentKeyToolsetSelection {
		return
	}
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/call" {
				params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
				if !ok || !m.toolListedForContext(ctx, params.Name) {
					return m.toolError(invalid("tool_not_available", "tool is outside the selected catalog")), nil
				}
			}
			result, err := next(ctx, method, req)
			if err != nil || method != "tools/list" {
				return result, err
			}
			page, ok := result.(*mcp.ListToolsResult)
			if !ok {
				return result, err
			}
			filtered := *page
			filtered.Tools = make([]*mcp.Tool, 0, len(page.Tools))
			for _, tool := range page.Tools {
				if m.toolListedForContext(ctx, tool.Name) {
					filtered.Tools = append(filtered.Tools, tool)
				}
			}
			return &filtered, nil
		}
	})
}
