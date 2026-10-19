package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func selectedManagedServiceProbe(ctx context.Context, client serviceBindingProbeClient, slug, service string) (*api.ManagedServiceProbeSpec, error) {
	reader, ok := client.(interface {
		ManagedServiceProbeSelection(string) (*api.ManagedServiceProbeSpec, error)
	})
	if !ok {
		return nil, nil
	}
	return reader.ManagedServiceProbeSelection(service)
}

func managedProbeFromInventory(i api.AppBindingInventory, slug, service string) (*api.ManagedServiceProbeSpec, error) {
	for _, item := range i.Bindings {
		if item.Type != api.BindingTypeService || item.Name != service || item.ServiceContext == nil {
			continue
		}
		spec, supported := api.ManagedServiceProbeForItem(item)
		if !supported || i.App != slug || i.ServiceBindingSource != "environment_release" || i.VerificationDeploymentID != spec.CallerDeploymentID || i.VerificationScope != spec.Scope || !i.Complete {
			return nil, fmt.Errorf("%s: select a supported active managed HTTPS service binding", api.ManagedServiceVerificationUnsupported)
		}
		return &spec, nil
	}
	return nil, nil
}

func (c *deploymentBindingProbeClient) ManagedServiceProbeSelection(service string) (*api.ManagedServiceProbeSpec, error) {
	return managedProbeFromInventory(c.inventory, c.slug, service)
}

type managedInventoryProbeClient struct {
	bindingInventoryProbeClient
	inventory api.AppBindingInventory
	slug      string
}

func (c *managedInventoryProbeClient) ManagedServiceProbeSelection(service string) (*api.ManagedServiceProbeSpec, error) {
	return managedProbeFromInventory(c.inventory, c.slug, service)
}

func (c *managedInventoryProbeClient) GetAppBindingInventoryForDeployment(ctx context.Context, slug, scope, deployment string) (api.AppBindingInventory, error) {
	reader, ok := c.bindingInventoryProbeClient.(interface {
		GetAppBindingInventoryForDeployment(context.Context, string, string, string) (api.AppBindingInventory, error)
	})
	if !ok {
		return api.AppBindingInventory{}, fmt.Errorf("managed service context refresh unsupported")
	}
	return reader.GetAppBindingInventoryForDeployment(ctx, slug, scope, deployment)
}

func confirmManagedServiceProbe(ctx context.Context, client serviceBindingProbeClient, slug string, spec api.ManagedServiceProbeSpec, report api.ServiceBindingProbeReport, task api.AppTaskResponse) error {
	if report.ManagedService == nil || *report.ManagedService != spec || report.TargetDeploymentID != spec.TargetDeploymentID || task.DeploymentID != spec.CallerDeploymentID || task.DeploymentScope != spec.Scope || task.ExitCode == nil || *task.ExitCode != 0 {
		return fmt.Errorf("managed service canary did not confirm the selected frozen context")
	}
	reader, ok := client.(interface {
		GetAppBindingInventoryForDeployment(context.Context, string, string, string) (api.AppBindingInventory, error)
	})
	if !ok {
		return fmt.Errorf("managed service context could not be refreshed")
	}
	i, err := reader.GetAppBindingInventoryForDeployment(ctx, slug, spec.Scope, spec.CallerDeploymentID)
	if err != nil {
		return fmt.Errorf("refresh managed service context: %w", err)
	}
	if i.App != slug || !i.Complete || i.ServiceBindingSource != "environment_release" || i.RequestedDeploymentID != spec.CallerDeploymentID || i.VerificationDeploymentID != spec.CallerDeploymentID || i.VerificationScope != spec.Scope {
		return fmt.Errorf("managed service inventory did not reconfirm the selected deployment")
	}
	for _, item := range i.Bindings {
		current, supported := api.ManagedServiceProbeForItem(item)
		if supported && current == spec && item.VerificationStatus == "passed" && item.Verification != nil && item.Verification.TaskID == task.ID && item.Verification.ManagedService != nil && *item.Verification.ManagedService == spec {
			return nil
		}
	}
	return fmt.Errorf("managed service context or evidence changed during verification; refresh and verify again")
}

func pinManagedServiceRequest(i api.AppBindingInventory, request *api.CreateAppTaskRequest) {
	if len(request.Command) != 2 || request.Command[0] != api.AppTaskServiceBindingProbeCommand {
		return
	}
	for _, item := range i.Bindings {
		if spec, ok := api.ManagedServiceProbeForItem(item); ok && spec.Service == request.Command[1] {
			request.Command = []string{api.AppTaskServiceBindingTargetProbeCommand, spec.Service, spec.TargetDeploymentID}
			request.VerificationDeploymentID = spec.CallerDeploymentID
			return
		}
	}
}
