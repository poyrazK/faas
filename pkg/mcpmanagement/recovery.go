// adr: 20261103193000
package mcpmanagement

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

const recoveryContractVersion = 1

// Recovery contains only known tool names and value-free selectors. It never
// copies native problem extensions, configuration values or original inputs.
type Recovery struct {
	Action       string         `json:"action"`
	Tool         string         `json:"tool,omitempty"`
	Arguments    map[string]any `json:"arguments,omitempty"`
	AfterSeconds *int64         `json:"after_seconds,omitempty"`
	ReuseInputs  bool           `json:"reuse_original_inputs,omitempty"`
	Toolsets     []string       `json:"toolsets,omitempty"`
}

type planFailure struct {
	Status  string    `json:"status"`
	Problem ToolError `json:"problem"`
}

func recoveryForError(p ToolError) Recovery {
	action := "inspect"
	switch {
	case strings.HasPrefix(p.Code, "plan_limit_") || p.Code == "plan_cron_quota":
		action = "replan"
	case p.Code == "quota_exhausted":
		action = "inspect"
	case p.Status == http.StatusUnauthorized || p.Status == http.StatusForbidden:
		action = "request_access"
	case p.Code == "writes_disabled" || p.Code == "capability_required" || p.Code == "approval_required" || p.Code == "account_changed" ||
		p.Code == "app_access_denied" || p.Code == "request_access_denied":
		action = "request_access"
	case p.Code == api.CodeEnvBatchRevisionConflict || p.Code == "plan_stale" || p.Code == "plan_expired" ||
		p.Code == "invalid_plan":
		action = "replan"
	}
	return Recovery{Action: action}
}

func (m *management) recoveryTool(r Recovery, tool string, args map[string]any) Recovery {
	if (strings.HasPrefix(tool, "gregale_apply_") || tool == "gregale_promote_deployment") && !m.opts.AllowWrites {
		return Recovery{Action: "request_access"}
	}
	capability := ""
	switch tool {
	case "gregale_plan_env_change", "gregale_apply_env_change", "gregale_plan_config_rollback":
		capability = api.ScopeEnvWrite
	case "gregale_plan_bucket":
		capability = api.ScopeStorageManage
	case "gregale_plan_postgres":
		capability = api.ScopeManagedPostgresManage
	}
	if capability != "" && !m.hasCapability(capability) {
		return Recovery{Action: "request_access"}
	}
	if m.toolNames[tool] {
		r.Tool, r.Arguments = tool, args
	} else if set, ok := toolsetOf[tool]; ok {
		r.Toolsets = []string{set}
	}
	return r
}

func plannerFor(p *releasePlan) string {
	if p.Operation != nil {
		switch p.Operation.Kind {
		case "env":
			return "gregale_plan_env_change"
		case "config_rollback":
			return "gregale_plan_config_rollback"
		case "cron_update", "cron_delete":
			return "gregale_plan_cron_change"
		case "domain_delete":
			return "gregale_plan_domain_removal"
		case "restart", "scale":
			return "gregale_plan_" + p.Operation.Kind
		}
	}
	if p.Provision != nil {
		return "gregale_plan_" + p.Provision.Kind
	}
	switch p.Action {
	case "deploy":
		if p.Source != nil {
			return "gregale_plan_source_deploy"
		}
		return "gregale_plan_deploy"
	case "local_deploy":
		return "gregale_plan_local_deploy"
	case "upload_deploy":
		return "gregale_plan_upload_deploy"
	case "rollback":
		return "gregale_plan_rollback"
	case "workload_rollout", "service_abort", "route_budgets", "regression_action":
		return "gregale_plan_" + p.Action
	}
	return ""
}

func retryPlanArguments(id, tool string) map[string]any {
	args := map[string]any{"plan_id": id}
	if tool == "gregale_apply_provision" {
		args["async"] = true
	}
	return args
}

func applyToolFor(p *releasePlan) string {
	if p.Operation != nil {
		if p.Operation.Kind == "env" {
			return "gregale_apply_env_change"
		}
		return "gregale_apply_operation"
	}
	if p.Provision != nil {
		return "gregale_apply_provision"
	}
	switch p.Action {
	case "deploy":
		return "gregale_apply_deploy"
	case "local_deploy":
		return "gregale_apply_local_deploy"
	case "upload_deploy":
		return "gregale_apply_upload_deploy"
	case "rollback":
		return "gregale_apply_rollback"
	case "workload_rollout":
		return "gregale_apply_workload_rollout"
	case "service_abort":
		return "gregale_apply_service_abort"
	}
	return ""
}

func safePlanRetry(p *releasePlan, tool string) bool {
	if p == nil {
		return false
	}
	if p.Provision != nil {
		return p.Provision.Native != nil
	}
	if p.Operation != nil {
		return p.Operation.Kind != "config_rollback"
	}
	return p.Action == "deploy" && p.Source == nil && tool == "gregale_apply_deploy"
}

// Caller holds m.mu. A known refusal is terminal for this reviewed plan;
// ambiguous attempts retain their original request and idempotency identity.
func (m *management) operationFailure(p *releasePlan, err error, priorAttempt bool) error {
	problem := m.toolError(err).StructuredContent.(ToolError)
	status := "outcome_unknown"
	if p.configReceipt != nil {
		status = "partial"
	} else if p.Operation.Kind == "scale" && knownScalingRefusal(err) {
		// Atomic receipts are returned before revision checks, so this native
		// conflict proves the reviewed request did not commit, even on retry.
		status = "refused"
	} else if !priorAttempt && p.Operation.Kind != "config_rollback" {
		var native *api.APIError
		if errors.As(err, &native) && native.Problem.Status >= 400 && native.Problem.Status < 500 &&
			native.Problem.Status != 408 && native.Problem.Status != 425 && native.Problem.Status != 429 {
			status = "refused"
		}
	}
	problem.Outcome = status
	p.failure = &planFailure{Status: status, Problem: problem}
	if saveErr := m.persistPlans(); saveErr != nil {
		return saveErr
	}
	return err
}

// toolErrorForCall adds deterministic recovery to the standard sanitized error.
// Its arguments are derived from a connection-local plan ID, never error prose.
func (m *management) toolErrorForCall(name string, args any, err error) *mcp.CallToolResult {
	result := m.toolError(err)
	p := result.StructuredContent.(ToolError)
	var lookup planLookupArgs
	raw, _ := json.Marshal(args)
	_ = json.Unmarshal(raw, &lookup)
	if lookup.PlanID != "" {
		m.mu.Lock()
		plan := m.plans[lookup.PlanID]
		if plan != nil && plan.failure != nil {
			p.Outcome = plan.failure.Status
			if p.Outcome == "refused" && p.Recovery.Action != "request_access" {
				p.Recovery = Recovery{Action: "replan", ReuseInputs: true}
			}
		}
		if p.Recovery.Action == "replan" && plan != nil {
			p.Recovery.ReuseInputs = true
			p.Recovery = m.recoveryTool(p.Recovery, plannerFor(plan), nil)
		} else if p.Recovery.Action != "request_access" && p.Recovery.Action != "replan" {
			if strings.HasPrefix(name, "gregale_apply_") || name == "gregale_promote_deployment" {
				if safePlanRetry(plan, name) && p.Code != "quota_exhausted" && (p.Status == 429 || p.Status >= 500 || p.Code == "timeout" || p.Code == "in_progress") {
					p.Recovery.Action = "retry_same_plan"
					p.Recovery = m.recoveryTool(p.Recovery, name, retryPlanArguments(lookup.PlanID, name))
				} else {
					p.Recovery = m.recoveryTool(p.Recovery, "gregale_get_plan", map[string]any{"plan_id": lookup.PlanID})
				}
			}
		}
		m.mu.Unlock()
	}
	if !m.toolWrites[name] && p.Recovery.Action == "inspect" && p.Code != api.CodeQuotaExhausted && (p.Status == 429 || p.Status >= 500 || p.Code == "timeout") {
		// Reads can be retried with their original selectors, which are not echoed.
		p.Recovery = Recovery{Action: "retry_read", Tool: name, ReuseInputs: true}
	}
	if p.Recovery.Action == "retry_same_plan" || p.Recovery.Action == "retry_read" {
		p.Recovery.AfterSeconds = p.RetryAfterSeconds
	}
	return errorResult(p)
}

func errorResult(p ToolError) *mcp.CallToolResult {
	data, _ := json.Marshal(p)
	return &mcp.CallToolResult{IsError: true, StructuredContent: p, Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
}

// Caller holds m.mu. Completed work needs no retry; all other suggestions
// preserve the existing plan ID unless a known refusal requires a new review.
func (m *management) receiptRecovery(p *releasePlan, status string) Recovery {
	args := retryPlanArguments(p.ID, applyToolFor(p))
	if p.failure != nil && p.failure.Problem.Code == api.CodeQuotaExhausted {
		return Recovery{Action: "inspect"}
	}
	if p.failure != nil && status != "completed" && status != "promoted" && recoveryForError(p.failure.Problem).Action == "request_access" {
		return Recovery{Action: "request_access"}
	}
	if p.Provision != nil && p.Provision.Native != nil && status != "completed" && status != "cancelled" {
		if problem := p.Provision.Native.Problem; problem != nil {
			if recoveryForError(*problem).Action == "request_access" {
				return Recovery{Action: "request_access"}
			}
			if problem.Code == "provision_failed" {
				return Recovery{Action: "inspect"}
			}
		}
	}
	switch status {
	case "completed", "promoted", "cancelled":
		return Recovery{Action: "none"}
	case "refused":
		r := recoveryForError(p.failure.Problem)
		if r.Action != "request_access" {
			r = Recovery{Action: "replan", ReuseInputs: true}
			r = m.recoveryTool(r, plannerFor(p), nil)
		}
		return r
	case "partial":
		if p.failure != nil && recoveryForError(p.failure.Problem).Action == "request_access" {
			return Recovery{Action: "request_access"}
		}
		return m.recoveryTool(Recovery{Action: "retry_same_plan"}, applyToolFor(p), args)
	case "cancellation_requested":
		return m.recoveryTool(Recovery{Action: "inspect"}, "gregale_get_provision", map[string]any{"plan_id": p.ID})
	case "interrupted", "outcome_unknown":
		// Source-bearing temporary CLI plans cannot safely be replayed blindly.
		if p.Operation != nil && p.Operation.Kind == "env" || p.Provision != nil && p.Provision.Native != nil || p.Action == "deploy" && p.Source == nil {
			return m.recoveryTool(Recovery{Action: "retry_same_plan"}, applyToolFor(p), args)
		}
		return Recovery{Action: "inspect"}
	default:
		return Recovery{Action: "inspect"}
	}
}

func (m *management) recoverableReceipt(p *releasePlan) planReceipt {
	r := receiptOf(p)
	verification, _ := m.planVerification(p)
	r.Verification = &verification
	r.Recovery = m.receiptRecovery(p, r.Status)
	if r.Problem != nil {
		problem := *r.Problem
		problem.Recovery = r.Recovery
		r.Problem = &problem
	}
	return r
}
