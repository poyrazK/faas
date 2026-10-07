// adr: 428 — pinned verification refuses silently ignored deployment selectors.
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type deploymentBindingProbeClient struct {
	*api.Client
	slug      string
	inventory api.AppBindingInventory
}

func resolveBindingDeployment(ctx context.Context, client *api.Client, slug, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	id, err := resolveDeploymentArg(ctx, client, slug, ref)
	if err != nil {
		return "", err
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", fmt.Errorf("resolve binding deployment: %w", err)
	}
	return parsed.String(), nil
}

func selectBindingProbeClient(ctx context.Context, client *api.Client, slug, ref string) (bindingInventoryProbeClient, error) {
	if ref == "" {
		return client, nil
	}
	id, err := resolveBindingDeployment(ctx, client, slug, ref)
	if err != nil {
		return nil, err
	}
	inventory, err := client.GetAppBindingInventoryForDeployment(ctx, slug, "", id)
	if err != nil {
		return nil, err
	}
	if inventory.App != slug || !sameBindingDeployment(inventory.RequestedDeploymentID, id) || !sameBindingDeployment(inventory.VerificationDeploymentID, id) || inventory.VerificationScope == "" {
		return nil, fmt.Errorf("server did not confirm the requested binding verification deployment")
	}
	return &deploymentBindingProbeClient{Client: client, slug: slug, inventory: inventory}, nil
}

func (c *deploymentBindingProbeClient) GetAppBindingInventory(_ context.Context, slug, scope string) (api.AppBindingInventory, error) {
	if slug != c.slug || scope != "" {
		return api.AppBindingInventory{}, fmt.Errorf("binding probe selection changed")
	}
	return c.inventory, nil
}

func (c *deploymentBindingProbeClient) GetApp(_ context.Context, slug string) (api.AppResponse, error) {
	if slug != c.slug {
		return api.AppResponse{}, fmt.Errorf("binding probe app changed")
	}
	app := api.AppResponse{}
	for _, item := range c.inventory.Bindings {
		if item.Type == api.BindingTypeService {
			app.ServiceBindings = append(app.ServiceBindings, api.AppServiceBinding{Service: item.Name, Binding: item.Binding})
		}
	}
	return app, nil
}

func (c *deploymentBindingProbeClient) CreateAppTask(ctx context.Context, slug string, request api.CreateAppTaskRequest) (api.AppTaskResponse, error) {
	if slug != c.slug || !api.IsBindingVerificationCommand(request.Command, request.CommandShell) {
		return api.AppTaskResponse{}, fmt.Errorf("deployment selection is limited to binding verification probes")
	}
	request.VerificationDeploymentID = c.inventory.RequestedDeploymentID
	task, err := c.Client.CreateAppTask(ctx, slug, request)
	if err == nil && (!sameBindingDeployment(task.DeploymentID, request.VerificationDeploymentID) || task.DeploymentScope != c.inventory.VerificationScope) {
		cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bindingProbeCancelTimeout)
		defer cancel()
		if _, cancelErr := c.CancelAppTask(cancelCtx, slug, task.ID); cancelErr != nil {
			PrintWarn(osStderr, "could not cancel mismatched binding probe task %s: %v", task.ID, cancelErr)
		}
		return task, fmt.Errorf("server admitted a different binding verification deployment or scope")
	}
	return task, err
}

func (c *deploymentBindingProbeClient) GetAppTask(ctx context.Context, slug, taskID string) (api.AppTaskResponse, error) {
	task, err := c.Client.GetAppTask(ctx, slug, taskID)
	if err == nil && (!sameBindingDeployment(task.DeploymentID, c.inventory.RequestedDeploymentID) || task.DeploymentScope != c.inventory.VerificationScope) {
		return task, fmt.Errorf("probe receipt belongs to another deployment or scope")
	}
	return task, err
}

func sameBindingDeployment(a, b string) bool {
	if a == b && a != "" {
		return true
	}
	aID, aErr := uuid.Parse(a)
	bID, bErr := uuid.Parse(b)
	return aErr == nil && bErr == nil && aID == bID
}

func validBindingDeploymentFlag(ref string) bool {
	return ref == "" || strings.TrimSpace(ref) == ref && validDeploymentRef(ref)
}
