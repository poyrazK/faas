// adr: 1029 — read-only preflight applies and confirms stored release policies.
package main

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
)

type bindingReleasePolicyCheckClient interface {
	bindingCheckInventoryClient
	GetApp(context.Context, string) (api.AppResponse, error)
	GetBindingReleasePolicy(context.Context, string, string) (api.BindingReleasePolicy, error)
}

// This is a CLI observation, never an authorization for a later traffic write.
// The server retains its atomic policy and catalog fences at promotion.
type bindingReleasePolicyCheckReport struct {
	bindingcheck.Report
	ReleasePolicy          *api.BindingReleasePolicy `json:"release_policy,omitempty"`
	ReleasePolicyConfirmed bool                      `json:"release_policy_confirmed"`
}

func pollBindingCheckReleasePolicy(ctx context.Context, client bindingReleasePolicyCheckClient, local bindingcheck.Policy, wait bool, interval time.Duration) (bindingReleasePolicyCheckReport, error) {
	var result bindingReleasePolicyCheckReport
	inventory, err := client.GetAppBindingInventoryForDeployment(ctx, local.App, local.Scope, local.DeploymentID)
	if err != nil {
		return result, err
	}
	result.Report, err = bindingcheck.Evaluate(inventory, local, time.Now())
	if err != nil {
		return result, err
	}
	result.Passed = false // No pass until a stored policy is evaluated and confirmed.
	if !validReleasePolicyCheckSelection(inventory, local) {
		return releasePolicyCheckFailure(result, "release_policy_selection_invalid", "The server did not confirm a live deployment and its exact scope.", nil)
	}
	app, err := client.GetApp(ctx, local.App)
	appID, parseErr := uuid.Parse(app.ID)
	if err != nil || parseErr != nil || appID == uuid.Nil || app.Slug != local.App {
		return releasePolicyCheckFailure(result, "release_policy_app_unconfirmed", "Could not confirm the app identity for its release policy.", err)
	}
	stored, err := client.GetBindingReleasePolicy(ctx, local.App, inventory.VerificationScope)
	if err != nil {
		return releasePolicyCheckFailure(result, "release_policy_unavailable", "Could not read the stored release policy; no local-policy fallback was used.", err)
	}
	if err := validateReleasePolicyCheckTarget(stored, appID.String(), inventory.VerificationScope); err != nil {
		return releasePolicyCheckFailure(result, "release_policy_invalid", "The stored release policy does not have valid app, scope or policy fields.", err)
	}
	stored = copyBindingReleasePolicy(stored)
	result.ReleasePolicy = &stored
	local.DeploymentID, local.Scope = inventory.VerificationDeploymentID, inventory.VerificationScope
	effective, err := effectiveBindingReleaseCheckPolicy(local, stored)
	if err != nil {
		return releasePolicyCheckFailure(result, "release_policy_check_invalid", "The stored and local requirements cannot form a valid check.", err)
	}
	// Discovery selected the target, but its evidence has not been evaluated
	// against the effective policy. Retain no local-only findings if the next
	// inventory read fails; still expose the exact requirements being checked.
	result.Report = bindingcheck.Report{App: effective.App, Scope: effective.Scope, DeploymentID: effective.DeploymentID,
		ExpectedDeploymentID: effective.DeploymentID, CheckedAt: time.Now().UTC(), MaxVerificationAge: effective.MaxVerificationAge.String(),
		AllowUnsupported: effective.AllowUnsupported, RequireApplicationAck: effective.RequireApplicationAck,
		ObjectStorageRequirements: maps.Clone(effective.ObjectStorageRequirements), Coverage: "none",
		QueueRequirements: maps.Clone(effective.QueueRequirements),
		Bindings:          []bindingcheck.BindingResult{}, Runtime: []api.BindingRuntimeDeployment{}, Issues: []api.BindingInventoryIssue{},
		Blockers: []bindingcheck.Finding{}, Warnings: []bindingcheck.Finding{}}
	confirm := func(ctx context.Context, report bindingcheck.Report) (bindingcheck.Report, error) {
		result.ReleasePolicyConfirmed = false
		current, err := client.GetBindingReleasePolicy(ctx, local.App, stored.Scope)
		if err != nil {
			return blockReleasePolicyCheck(report, "release_policy_unavailable", "Could not confirm the stored release policy after reading binding evidence."), err
		}
		if err := validateReleasePolicyCheckTarget(current, stored.AppID, stored.Scope); err != nil {
			return blockReleasePolicyCheck(report, "release_policy_invalid", "The confirmation response has invalid policy fields or belongs to another app or scope."), err
		}
		if !sameBindingReleasePolicy(stored, current) {
			message := "The release policy changed during preflight; run the check again against the new revision."
			return blockReleasePolicyCheck(report, "release_policy_changed", message), fmt.Errorf("%s", message)
		}
		if err := ctx.Err(); err != nil {
			return blockReleasePolicyCheck(report, "release_policy_check_incomplete", "Preflight ended before the release policy could be confirmed."), err
		}
		result.ReleasePolicyConfirmed = true
		return report, nil
	}
	report, err := pollBindingCheckConfirmed(ctx, client, effective, wait, interval, confirm)
	if report.App != "" {
		result.Report = report
	}
	if err != nil {
		result.ReleasePolicyConfirmed = false
		if len(result.Blockers) == 0 {
			result.Report = blockReleasePolicyCheck(result.Report, "release_policy_check_incomplete", "Preflight did not complete; no release-policy pass was confirmed.")
		}
		result.Passed = false
	}
	return result, err
}

func validReleasePolicyCheckSelection(inventory api.AppBindingInventory, local bindingcheck.Policy) bool {
	id, err := uuid.Parse(inventory.VerificationDeploymentID)
	return err == nil && id != uuid.Nil && inventory.App == local.App && inventory.Scope == local.Scope && api.ValidateScope(inventory.VerificationScope) == nil &&
		(local.Scope == "" || local.Scope == inventory.VerificationScope) &&
		(local.DeploymentID == "" || sameBindingDeployment(local.DeploymentID, inventory.RequestedDeploymentID) && sameBindingDeployment(local.DeploymentID, inventory.VerificationDeploymentID)) &&
		(inventory.RequestedDeploymentID == "" || sameBindingDeployment(inventory.RequestedDeploymentID, inventory.VerificationDeploymentID))
}

func validateReleasePolicyCheckTarget(policy api.BindingReleasePolicy, appID, scope string) error {
	if err := validateBindingReleasePolicyResult(policy, scope); err != nil {
		return err
	}
	if !sameBindingDeployment(policy.AppID, appID) {
		return fmt.Errorf("release policy app identity mismatch")
	}
	return nil
}

func effectiveBindingReleaseCheckPolicy(local bindingcheck.Policy, stored api.BindingReleasePolicy) (bindingcheck.Policy, error) {
	local.ObjectStorageRequirements = maps.Clone(local.ObjectStorageRequirements)
	local.QueueRequirements = maps.Clone(local.QueueRequirements)
	if stored.Mode == "enforce" {
		age, _ := time.ParseDuration(stored.MaxVerificationAge) // Validated before use.
		if local.MaxVerificationAge > age {
			local.MaxVerificationAge = age
		}
		local.AllowUnsupported = false
		local.RequireApplicationAck = local.RequireApplicationAck || stored.RequireApplicationAck
		if local.ObjectStorageRequirements == nil {
			local.ObjectStorageRequirements = map[string]string{}
		}
		for prefix, mode := range stored.ObjectStorageRequirements {
			if requested := local.ObjectStorageRequirements[prefix]; requested != "" && requested != mode {
				mode = "read_write" // Covers both read and write, including either combined mode.
			}
			local.ObjectStorageRequirements[prefix] = mode
		}
		if local.QueueRequirements == nil {
			local.QueueRequirements = map[string]string{}
		}
		for name, evidence := range stored.QueueRequirements {
			local.QueueRequirements[name] = evidence
		}
	}
	if err := api.ValidateQueueReleaseRequirements(local.QueueRequirements); err != nil {
		return local, err
	}
	return local, api.ValidateObjectStorageRequirements(local.ObjectStorageRequirements)
}

func copyBindingReleasePolicy(policy api.BindingReleasePolicy) api.BindingReleasePolicy {
	policy.ObjectStorageRequirements = maps.Clone(policy.ObjectStorageRequirements)
	policy.QueueRequirements = maps.Clone(policy.QueueRequirements)
	if policy.UpdatedAt != nil {
		updated := *policy.UpdatedAt
		policy.UpdatedAt = &updated
	}
	return policy
}

func sameBindingReleasePolicy(a, b api.BindingReleasePolicy) bool {
	aAge, _ := time.ParseDuration(a.MaxVerificationAge)
	bAge, _ := time.ParseDuration(b.MaxVerificationAge)
	updatedEqual := a.UpdatedAt == nil && b.UpdatedAt == nil || a.UpdatedAt != nil && b.UpdatedAt != nil && a.UpdatedAt.Equal(*b.UpdatedAt)
	return sameBindingDeployment(a.AppID, b.AppID) && a.Scope == b.Scope && a.Revision == b.Revision && a.Mode == b.Mode && aAge == bAge &&
		a.RequireApplicationAck == b.RequireApplicationAck && maps.Equal(a.ObjectStorageRequirements, b.ObjectStorageRequirements) && maps.Equal(a.QueueRequirements, b.QueueRequirements) && a.Reason == b.Reason && updatedEqual
}

func blockReleasePolicyCheck(report bindingcheck.Report, code, message string) bindingcheck.Report {
	report.Passed = false
	report.Blockers = append(report.Blockers, bindingcheck.Finding{Code: code, Scope: report.Scope, DeploymentID: report.DeploymentID, Message: message})
	return report
}

func releasePolicyCheckFailure(result bindingReleasePolicyCheckReport, code, message string, err error) (bindingReleasePolicyCheckReport, error) {
	result.Report = blockReleasePolicyCheck(result.Report, code, message)
	if err != nil {
		return result, fmt.Errorf("%s: %w", message, err)
	}
	return result, fmt.Errorf("%s", message)
}
