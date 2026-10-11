package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
)

const bindingCheckPollIntervalDefault = time.Second

type bindingCheckInventoryClient interface {
	GetAppBindingInventoryForDeployment(context.Context, string, string, string) (api.AppBindingInventory, error)
}

// Waiting only observes work already scheduled by the customer or control plane.
// Pin the first selected deployment and scope so a rollout cannot change the check.
func pollBindingCheck(ctx context.Context, client bindingCheckInventoryClient, policy bindingcheck.Policy, wait bool, interval time.Duration) (bindingcheck.Report, error) {
	var last bindingcheck.Report
	for {
		inventory, err := client.GetAppBindingInventoryForDeployment(ctx, policy.App, policy.Scope, policy.DeploymentID)
		if err != nil {
			return last, err
		}
		last, err = bindingcheck.Evaluate(inventory, policy, time.Now())
		if err != nil {
			return last, err
		}
		if !wait || last.Passed || !bindingCheckCanProgress(last, inventory) {
			return last, nil
		}
		if policy.DeploymentID == "" {
			policy.DeploymentID = last.DeploymentID
		}
		if policy.Scope == "" {
			policy.Scope = last.Scope
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, ctx.Err()
		case <-timer.C:
		}
	}
}

func bindingCheckCanProgress(report bindingcheck.Report, inventory api.AppBindingInventory) bool {
	if len(report.Blockers) == 0 {
		return false
	}
	for _, blocker := range report.Blockers {
		switch blocker.Code {
		case "runtime_updating", "runtime_stale", "rotation_pending", "application_ack_stale", "application_ack_candidate_unobserved":
			continue
		case "verification_unknown", "refresh_incomplete", "application_adoption_unknown":
			pending := false
			for _, item := range inventory.Bindings {
				if item.Type != blocker.Type || item.Name != blocker.Name || item.Binding != blocker.Binding || item.Scope != blocker.Scope {
					continue
				}
				switch blocker.Code {
				case "verification_unknown":
					pending = item.Verification != nil && item.Verification.Reason == "probe_pending" && item.Verification.Result == "unknown"
				case "refresh_incomplete":
					pending = item.Refresh != nil && (item.Refresh.Status == "queued" || item.Refresh.Status == "running" || item.Refresh.Status == "retrying")
				case "application_adoption_unknown":
					pending = bindingApplicationAckPending(item, report.CheckedAt)
				}
			}
			if !pending {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func bindingApplicationAckPending(item api.AppBindingInventoryItem, now time.Time) bool {
	input := item.ApplicationAdoption
	if input == nil || !input.Complete || input.Source != "application_ack" || len(input.Targets) == 0 {
		return false
	}
	adoption := bindingcheck.SummarizeApplicationAdoption(*input, now)
	if !adoption.Complete || adoption.Status == "failed" || adoption.ObservedAt.IsZero() || adoption.ObservedAt.After(now) || adoption.SecretsExpected <= 0 || adoption.SecretsExpected != len(api.BindingCredentialSecretKeys(item.Type, item.Binding)) || adoption.SecretsExpected != adoption.SecretsObserved {
		return false
	}
	for _, target := range adoption.Targets {
		if target.ReloadSupport != "enabled" {
			return false
		}
		switch target.ApplicationAckReason {
		case "current", "application_ack_missing", "application_ack_generation_missing", "application_ack_generation_mismatch", "application_ack_stale":
		default:
			return false
		}
		switch target.ReloadReason {
		case "current", "reload_observation_missing", "reload_stale":
		default:
			return false
		}
	}
	return true
}
