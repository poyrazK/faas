package bindingcheck

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (r *Report) checkVerification(item api.AppBindingInventoryItem, maxAge time.Duration) {
	switch item.VerificationStatus {
	case "passed":
		// Continue with the independently reported evidence identity and age.
	case "failed":
		r.bindingBlock(item, "verification_failed", "The latest probe failed; fix the binding and run bindings verify again.")
		return
	case "stale":
		r.bindingBlock(item, "verification_stale", "The deployment or binding configuration changed; run bindings verify again.")
		return
	default:
		r.bindingBlock(item, "verification_unknown", "Passed verification evidence is missing or pending; run bindings verify and wait for completion.")
		return
	}
	v := item.Verification
	if v == nil || v.Result != "passed" || v.Source != "task_guest" || v.Reason != "" {
		r.bindingBlock(item, "verification_unknown", "The reported pass has incomplete or contradictory evidence.")
		return
	}
	if !validVerificationChecks(item.Type, v.Checks) {
		r.bindingBlock(item, "verification_unknown", "The reported pass has missing, duplicated or unsuccessful probe stages.")
		return
	}
	if v.DeploymentID == "" || v.DeploymentID != r.DeploymentID {
		r.bindingBlock(item, "verification_deployment_mismatch", "Probe evidence belongs to a different deployment; run bindings verify again.")
	}
	if v.Scope != r.Scope {
		r.bindingBlock(item, "verification_scope_mismatch", "Probe evidence belongs to a different scope; verify the selected deployment scope.")
	}
	switch {
	case v.CheckedAt == nil || v.CheckedAt.IsZero():
		r.bindingBlock(item, "verification_time_unknown", "Probe completion time is missing; run bindings verify again.")
	case v.CheckedAt.After(r.CheckedAt):
		r.bindingBlock(item, "verification_time_future", "Probe completion time is in the future; check clock synchronization before rerunning preflight.")
	case r.CheckedAt.Sub(*v.CheckedAt) > maxAge:
		r.bindingBlock(item, "verification_expired", "Probe evidence exceeds --max-verification-age; run bindings verify again.")
	}
	if item.CredentialGeneration != nil && (v.CredentialGeneration == nil || *v.CredentialGeneration != *item.CredentialGeneration) {
		r.bindingBlock(item, "verification_generation_mismatch", "Probe evidence does not match the current credential generation; run bindings verify again.")
	}
}

func validVerificationChecks(kind string, checks []api.BindingVerificationCheck) bool {
	var names []string
	switch kind {
	case api.BindingTypeService:
		names = []string{"dns", "tls", "authorization", "routing"}
	case api.BindingTypePostgres:
		names = []string{"environment", "configuration", "connection", "query"}
	case api.BindingTypeOutbound:
		names = []string{"configuration", "identity", "gateway", "response"}
	case api.BindingTypeObjectStorage:
		names = []string{"environment", "configuration", "connection", "authorization", "bucket_access"}
	default:
		return false
	}
	if len(checks) != len(names) {
		return false
	}
	expected := make(map[string]bool, len(names))
	for _, name := range names {
		expected[name] = true
	}
	for _, check := range checks {
		if !expected[check.Name] || check.Status != "passed" {
			return false
		}
		delete(expected, check.Name)
	}
	return len(expected) == 0
}

func (r *Report) checkRotation(item api.AppBindingInventoryItem) {
	if item.RotationPending == nil {
		r.bindingBlock(item, "rotation_unknown", "Credential rotation state is unknown.")
	} else if *item.RotationPending {
		r.bindingBlock(item, "rotation_pending", "Credential retirement is still pending; wait for rotation to finish and check again.")
	}
	refresh := item.Refresh
	if refresh == nil {
		return
	}
	switch refresh.Status {
	case "completed":
		// Handoff completion cannot clear pending retirement or runtime blockers.
	case "failed":
		r.bindingBlock(item, "refresh_failed", "The credential refresh handoff failed; inspect rotation progress and recover it.")
	case "queued", "retrying", "running", "not_queued":
		r.bindingBlock(item, "refresh_incomplete", "The credential refresh handoff has not completed.")
	default:
		r.bindingBlock(item, "refresh_unknown", "The credential refresh handoff state is unknown.")
	}
}
