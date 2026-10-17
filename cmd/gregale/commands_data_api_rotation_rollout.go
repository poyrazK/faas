// adr: 1668 — metadata observations, never credential-use or revocation proofs.
package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPIRotationRollout struct {
	BindingID            string                       `json:"binding_id"`
	Scope                string                       `json:"scope"`
	ObservedAt           time.Time                    `json:"observed_at"`
	Complete             bool                         `json:"complete"`
	BindingState         string                       `json:"binding_state"`
	LastErrorCode        string                       `json:"last_error_code,omitempty"`
	CredentialGeneration int64                        `json:"credential_generation"`
	RotationPending      bool                         `json:"rotation_pending"`
	State                string                       `json:"state"`
	Retirement           string                       `json:"retirement"`
	Refresh              *api.BindingRefresh          `json:"refresh,omitempty"`
	RuntimeFreshness     *api.BindingRuntimeFreshness `json:"runtime_freshness,omitempty"`
	NextAction           string                       `json:"next_action"`
	Limitation           string                       `json:"limitation"`
}

func readDataAPIRotationRollout(ctx context.Context, client *api.Client, app, bindingID, deployment string) (dataAPIRotationRollout, error) {
	var result dataAPIRotationRollout
	application, err := client.GetApp(ctx, app)
	if err != nil {
		return result, err
	}
	binding, err := client.GetManagedPostgresBinding(ctx, bindingID)
	if err != nil {
		return result, err
	}
	if application.ID == "" || binding.ID != bindingID || binding.AppID != application.ID || binding.Access != "data_api" || binding.EnvironmentKey == "" || binding.Scope == "" || binding.CredentialGeneration < 1 {
		return result, errors.New("select a Data API PostgreSQL binding belonging to this app")
	}
	inventory, err := client.GetAppBindingInventoryForDeployment(ctx, app, binding.Scope, deployment)
	if err != nil {
		return result, err
	}
	return describeDataAPIRotationRollout(app, binding, inventory), nil
}

func describeDataAPIRotationRollout(app string, binding api.ManagedPostgresBinding, inventory api.AppBindingInventory) dataAPIRotationRollout {
	result := dataAPIRotationRollout{BindingID: binding.ID, Scope: binding.Scope, ObservedAt: inventory.GeneratedAt,
		BindingState: binding.State, LastErrorCode: binding.LastErrorCode, CredentialGeneration: binding.CredentialGeneration, RotationPending: binding.RotationPending,
		Complete: inventory.Complete && inventory.App == app && inventory.Scope == binding.Scope,
		State:    "unknown", Retirement: "unknown", NextAction: "Retry status with complete binding inventory before drawing conclusions about rollout or retirement.",
		Limitation: "Best-effort metadata from separate reads. Runtime timestamps do not prove readiness or credential use; refresh completion does not prove provider revocation. No previous generation pending describes catalog bookkeeping, including bindings that never rotated."}
	var selected *api.AppBindingInventoryItem
	for i := range inventory.Bindings {
		item := &inventory.Bindings[i]
		if item.Type == api.BindingTypePostgres && item.Binding == binding.EnvironmentKey && item.Scope == binding.Scope && item.Access == "data_api" {
			if selected != nil {
				result.Complete = false
				return result
			}
			selected = item
		}
	}
	if selected == nil || selected.CredentialGeneration == nil || selected.RotationPending == nil {
		result.Complete = false
		return result
	}
	if *selected.CredentialGeneration != binding.CredentialGeneration || *selected.RotationPending != binding.RotationPending || selected.State != binding.State {
		result.Complete, result.State = false, "changed_during_read"
		return result
	}
	result.Refresh, result.RuntimeFreshness = selected.Refresh, inventory.RuntimeFreshness
	if !result.Complete {
		return result
	}
	if !binding.RotationPending {
		result.State, result.Retirement = "idle", "no_previous_generation_pending"
		result.NextAction = "No predecessor cleanup is recorded as pending. Run rotation preflight before requesting another rotation."
		if binding.State != "ready" {
			result.State = "binding_unavailable"
			result.NextAction = "Inspect the binding state and error code, then repair provisioning before requesting rotation."
		}
		return result
	}
	result.State, result.Retirement = "rollout_pending", "awaiting_rollout"
	result.NextAction = "Wait for the existing rolling refresh and predecessor cleanup; do not request another rotation. Inspect bindings check for runtime and probe evidence."
	if binding.State == "retiring" {
		result.State, result.Retirement = "retiring", "in_progress"
		result.NextAction = "Wait for managed predecessor revocation to finish; inspect the binding error code if retirement remains pending."
	} else if result.Refresh != nil {
		switch result.Refresh.Status {
		case "failed", "cancelled":
			result.State = "rollout_failed"
			result.NextAction = "Inspect the recorded refresh failure and repair the current rollout before retrying; keep the predecessor until managed cleanup completes."
		case "completed":
			result.State, result.Retirement = "cleanup_pending", "pending"
			result.NextAction = "The refresh handoff completed; wait for managed predecessor retirement. Completion alone does not prove fresh runtimes or credential use."
		}
	}
	return result
}
