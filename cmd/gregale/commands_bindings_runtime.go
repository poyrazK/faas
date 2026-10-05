package main

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func humanBindingRuntimeFreshness(runtime *api.BindingRuntimeFreshness, binding api.AppBindingInventoryItem) string {
	if runtime == nil {
		return "unknown"
	}
	status := "inactive"
	appWide := binding.Type == api.BindingTypeService || binding.Type == api.BindingTypeQueue || binding.Type == api.BindingTypeOutbound
	for _, deployment := range runtime.Deployments {
		if !appWide && binding.Scope != deployment.Scope {
			continue
		}
		if bindingRuntimeStatusPriority(deployment.Status) > bindingRuntimeStatusPriority(status) {
			status = deployment.Status
		}
	}
	return status
}

func bindingRuntimeStatusPriority(status string) int {
	switch status {
	case "stale":
		return 4
	case "unknown":
		return 3
	case "updating":
		return 2
	case "current":
		return 1
	default:
		return 0
	}
}

func humanBindingRefresh(refresh *api.BindingRefresh) string {
	if refresh == nil {
		return "-"
	}
	if refresh.FailureReason != "" {
		return refresh.Status + " (" + refresh.FailureReason + ")"
	}
	return humanBindingValue(refresh.Status)
}

func renderBindingRuntimeFreshness(inventory api.AppBindingInventory) {
	runtime := inventory.RuntimeFreshness
	if runtime == nil {
		_, _ = fmt.Fprintln(osStdout, "Runtime configuration freshness: unknown.")
		return
	}
	for _, deployment := range runtime.Deployments {
		_, _ = fmt.Fprintf(osStdout, "runtime %s scope=%s deployment=%s (%s): serving current=%d stale=%d unknown=%d; resident current=%d stale=%d unknown=%d; starting=%d\n",
			deployment.Status, deployment.Scope, deployment.DeploymentID, deployment.DeploymentStatus,
			deployment.Serving.Current, deployment.Serving.Stale, deployment.Serving.Unknown,
			deployment.Resident.Current, deployment.Resident.Stale, deployment.Resident.Unknown, deployment.Starting)
		if deployment.Serving.Stale > 0 {
			_, _ = fmt.Fprintf(osStdout, "Warning: %d serving instances in scope %s predate the configuration change; a passing verification does not refresh them.\n",
				deployment.Serving.Stale, deployment.Scope)
		}
	}
	if len(runtime.Deployments) == 0 {
		_, _ = fmt.Fprintln(osStdout, "Runtime configuration freshness: inactive; no live deployments or resident app instances in the selected scope.")
	}
	_, _ = fmt.Fprintln(osStdout, "Configuration freshness uses instance start timestamps; it does not confirm credential use or connectivity.")
}
