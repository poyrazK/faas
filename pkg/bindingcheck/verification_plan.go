package bindingcheck

import (
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Use the release evaluator for freshness, stage coverage and scope decisions.
// The plan never manufactures evidence or interprets task output independently.
func VerificationPlan(inventory api.AppBindingInventory, stored api.BindingReleasePolicy, now time.Time) ([]api.BindingVerificationPlanStep, []Finding, *Report, error) {
	plan := []api.BindingVerificationPlanStep{}
	if stored.Mode != "enforce" {
		return plan, nil, nil, fmt.Errorf("an enforced release policy is required")
	}
	age, err := time.ParseDuration(stored.MaxVerificationAge)
	if err != nil {
		return plan, nil, nil, fmt.Errorf("parse release verification age: %w", err)
	}
	policy := Policy{App: inventory.App, Scope: stored.Scope, DeploymentID: inventory.VerificationDeploymentID, MaxVerificationAge: age, RequireApplicationAck: stored.RequireApplicationAck, ObjectStorageRequirements: stored.ObjectStorageRequirements, QueueRequirements: stored.QueueRequirements}
	report, err := Evaluate(inventory, policy, now)
	if err != nil {
		return plan, nil, nil, err
	}
	blockers := []Finding{}
	for _, finding := range report.Blockers {
		if bindingPolicyPlanBlocker(finding.Code) {
			blockers = append(blockers, finding)
		}
	}
	selected := map[string]int{}
	for _, item := range inventory.Bindings {
		step := bindingPolicyPlanItem(item, stored, report)
		if step.Action != "skip" {
			key := bindingPolicySelectionKey(step)
			selected[key]++
			if selected[key] == 2 {
				blockers = append(blockers, Finding{Type: item.Type, Name: item.Name, Binding: item.Binding, Scope: item.Scope, Code: "binding_selection_ambiguous", Message: "The canary selector does not identify one binding in the selected scope."})
			}
		}
		if step.Action == "probe" && !validBindingPolicyProbeSelection(item) {
			step.Action, step.Reason = "blocked", "binding_selection_invalid"
			blockers = append(blockers, Finding{Type: item.Type, Name: item.Name, Binding: item.Binding, Scope: item.Scope, Code: step.Reason, Message: "The binding does not publish a valid canary selector or permission."})
		}
		plan = append(plan, step)
	}
	sort.Slice(plan, func(i, j int) bool {
		a, b := plan[i], plan[j]
		return a.Type+"\x00"+a.Binding+"\x00"+a.Name+"\x00"+a.Scope < b.Type+"\x00"+b.Binding+"\x00"+b.Name+"\x00"+b.Scope
	})
	if len(blockers) > 0 {
		return plan, blockers, &report, fmt.Errorf("the verification plan has blockers; inspect plan_blockers")
	}
	return plan, blockers, &report, nil
}

func bindingPolicyPlanItem(item api.AppBindingInventoryItem, stored api.BindingReleasePolicy, report Report) api.BindingVerificationPlanStep {
	step := api.BindingVerificationPlanStep{TargetDeploymentID: item.ServiceTargetDeploymentID, Type: item.Type, Name: item.Name, Binding: item.Binding, Scope: item.Scope, QueueEnvironment: item.QueueEnvironment, Action: "reuse", Reason: "evidence_current"}
	for _, result := range report.Bindings {
		if result.Type == item.Type && result.Name == item.Name && result.Binding == item.Binding && result.Scope == item.Scope && result.Status == "skipped" {
			step.Action, step.Reason = "skip", result.Reason
			return step
		}
	}
	if item.Type == api.BindingTypeObjectStorage {
		step.Prefix, step.RequiredMode, step.ProbeMode, step.Operations = item.Binding, stored.ObjectStorageRequirements[item.Binding], "read", []string{"list"}
		if step.RequiredMode == "write" || step.RequiredMode == "read_write" || item.Access == "write" {
			step.ProbeMode, step.Operations = item.Access, []string{"upload", "cleanup"}
			if item.Access == "read_write" {
				step.Operations = []string{"upload", "download", "cleanup"}
			}
		}
	}
	if item.Type == api.BindingTypeQueue {
		step.Action, step.Reason, step.RequiredEvidence = "observe", "consumer_readiness", stored.QueueRequirements[item.Binding]
		step.CheckedAt = item.ObservedAt
		if step.RequiredEvidence == api.QueueEvidenceHandlerCompletion {
			step.Action, step.Reason = "queue-canary", "handler_completion"
			if item.QueueCanary != nil {
				switch item.QueueCanary.Status {
				case "succeeded":
					step.Action, step.Reason = "reuse", "handler_completion"
					step.CheckedAt = item.QueueCanary.CompletedAt
				case "published", "dispatching":
					step.Action, step.Reason = "wait-queue", "canary_pending"
				}
			}
		}
	} else if item.Verification != nil {
		step.CheckedAt = item.Verification.CheckedAt
	}
	for _, finding := range report.Blockers {
		if finding.Type != item.Type || finding.Name != item.Name || finding.Binding != item.Binding || finding.Scope != item.Scope {
			continue
		}
		if bindingPolicyPlanBlocker(finding.Code) {
			step.Action, step.Reason = "blocked", finding.Code
			return step
		}
		if item.Type == api.BindingTypeQueue && (finding.Code == "queue_completion_unverified" || finding.Code == "queue_completion_expired") && step.Action != "wait-queue" {
			step.Action = "queue-canary"
		}
		if bindingPolicyEvidenceBlocker(finding.Code) && step.Action != "probe" {
			step.Action, step.Reason = "probe", finding.Code
		}
		if item.Type == api.BindingTypeQueue {
			step.Reason = finding.Code
		}
	}
	if step.Action == "probe" && bindingPolicyPendingProbeCovers(item, step, report) {
		step.Action, step.Reason = "wait", "probe_pending"
	}
	return step
}

func bindingPolicyPendingProbeCovers(item api.AppBindingInventoryItem, step api.BindingVerificationPlanStep, report Report) bool {
	v := item.Verification
	if spec, managed := api.ManagedServiceProbeForItem(item); managed && (v == nil || v.ManagedService == nil || *v.ManagedService != spec) {
		return false
	}
	if item.Type == api.BindingTypeService && (v == nil || v.TargetDeploymentID != item.ServiceTargetDeploymentID) {
		return false
	}
	if item.VerificationStatus != "unknown" || v == nil || v.Result != "unknown" || v.Reason != "probe_pending" || v.Source != "task_guest" || v.DeploymentID != report.DeploymentID || v.Scope != report.Scope {
		return false
	}
	if item.CredentialGeneration != nil && (v.CredentialGeneration == nil || *v.CredentialGeneration != *item.CredentialGeneration) {
		return false
	}
	if item.Type == api.BindingTypeObjectStorage {
		required := step.RequiredMode
		if required == "" {
			required = step.ProbeMode
		}
		actual := v.ObjectStorageMode
		if actual == "" {
			actual = "read"
		}
		return actual == required || actual == "read_write"
	}
	return true
}

func bindingPolicySelectionKey(step api.BindingVerificationPlanStep) string {
	selector := step.Binding
	if step.Type == api.BindingTypeService || step.Type == api.BindingTypeOutbound {
		selector = step.Name
	}
	return step.Type + "\x00" + selector + "\x00" + step.Scope
}

func validBindingPolicyProbeSelection(item api.AppBindingInventoryItem) bool {
	switch item.Type {
	case api.BindingTypeService:
		names, err := api.NormalizeServiceBindingTargets([]string{item.Name})
		return err == nil && len(names) == 1 && names[0] == item.Name
	case api.BindingTypePostgres:
		return api.ValidateEnvKey(item.Binding) == nil && item.Access != "migration"
	case api.BindingTypeObjectStorage:
		return api.ValidObjectStorageBindingPrefix(item.Binding) && (item.Access == "read" || item.Access == "write" || item.Access == "read_write")
	case api.BindingTypeOutbound:
		return item.OutboundProbe != nil && item.OutboundProbe.Valid()
	}
	return false
}

func bindingPolicyEvidenceBlocker(code string) bool {
	switch code {
	case "service_target_unverified", "verification_failed", "verification_stale", "verification_unknown", "verification_deployment_mismatch", "verification_scope_mismatch", "verification_time_unknown", "verification_time_future", "verification_expired", "verification_generation_mismatch", "object_storage_operations_missing", "object_storage_operations_incomplete":
		return true
	}
	return false
}

func bindingPolicyPlanBlocker(code string) bool {
	switch code {
	case api.ManagedServiceVerificationUnsupported:
		return true
	case "queue_canary_context_unavailable", "queue_handler_unsupported", "queue_canary_scope_unsupported", "queue_canary_unsupported", "binding_not_ready", "binding_type_unknown", "verification_unsupported", "verification_unavailable", "rotation_unknown", "rotation_pending", "object_storage_permission_insufficient", "object_storage_binding_missing", "object_storage_binding_ambiguous", "queue_binding_missing", "queue_binding_ambiguous", "queue_scope_unconfirmed", "queue_readiness_unsupported", "queue_consumer_missing", "queue_consumer_paused", "queue_consumer_degraded", "queue_consumer_unknown", "queue_consumer_time_future":
		return true
	}
	return false
}
