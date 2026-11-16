package mcpmanagement

import "github.com/modelcontextprotocol/go-sdk/mcp"

// Task inspection has no dependency on the local adapter or publishing consent.
// Hosted access retains the native journal's original-requester restriction.
func (m *management) taskInspectionAccess() error {
	if !m.remote() {
		return m.taskIntentAccess()
	}
	if !m.opts.AllowTaskInspection || !m.opts.agentKey || m.opts.Environments != nil || m.opts.AllowedApps != nil || m.opts.Org != "" || m.opts.accountID == "" {
		return invalid("task_inspection_access_denied", "Task inspection requires an enabled unscoped account agent key and the original native requester identity")
	}
	return nil
}

func registerTaskInspection(s *mcp.Server, m *management) {
	if m.taskInspectionAccess() != nil {
		return
	}
	register(s, m, "gregale_inspect_task_tool_recovery", "Read durable Task checkpoints and fresh native deployment, serving, instance and observer facts after interruption. Uses only native GET APIs and needs no local files or bindings. Includes validated handover, restore, quarantine and retirement progress, separate from live health. Missing, ambiguous or changing evidence remains unknown; never repeats deployment or customer code.", m.inspectTaskRecovery)
	register(s, m, "gregale_plan_task_tool_recovery", "Produce an expiring, hashed read-only recovery plan from fresh native Task evidence. Lists blockers and owner checks for resume, restore, quarantine and retire, plus a lifecycle-aware next step. Fenced executors cannot resume; pending cleanup recommends observation and completed work cannot be reapplied. A plan never grants execution, renews approval, takes over a claim or changes traffic.", m.planTaskRecovery)
}
