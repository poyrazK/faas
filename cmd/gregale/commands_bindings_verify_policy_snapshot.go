package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
)

// A best-effort CLI observation, not a catalog lock or provider-policy lease.
// Compare the app-wide stamp as well as public metadata: credentials and
// private binding identities are intentionally absent from the inventory.
type bindingPolicySnapshotClient struct {
	*api.Client
	slug                  string
	policy                api.BindingReleasePolicy
	inventory             api.AppBindingInventory
	outboundIDs           map[string]string
	outboundConfiguration string
}

type bindingPolicySnapshotError struct {
	code, message string
	err           error
}

func (e *bindingPolicySnapshotError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("%s: %v", e.message, e.err)
	}
	return e.message
}

func (e *bindingPolicySnapshotError) Unwrap() error { return e.err }

func bindingPolicySnapshotFailure(code, message string, err error) error {
	return &bindingPolicySnapshotError{code: code, message: message, err: err}
}

func prepareBindingPolicyVerification(ctx context.Context, client *api.Client, slug, ref string, selections ...map[string]string) (*bindingPolicySnapshotClient, error) {
	if prepared, ok := ctx.Value(bindingVerificationSnapshotKey{}).(*bindingPolicySnapshotClient); ok {
		if prepared.slug != slug || !sameBindingDeployment(prepared.inventory.VerificationDeploymentID, ref) {
			return nil, errors.New("prepared verification snapshot does not match the selected member")
		}
		return prepared, nil
	}
	id, err := resolveBindingDeployment(ctx, client, slug, ref)
	if err != nil {
		return nil, fmt.Errorf("resolve verification deployment: %w", err)
	}
	var targets map[string]string
	if len(selections) > 0 {
		targets = maps.Clone(selections[0])
	}
	inventory, err := client.GetAppBindingInventoryForServiceTargets(ctx, slug, "", id, targets)
	if err != nil {
		return nil, fmt.Errorf("read verification inventory: %w", err)
	}
	if !validBindingPolicyServiceTargets(inventory, targets) || !validReleasePolicyCheckSelection(inventory, bindingcheck.Policy{App: slug, DeploymentID: id}) || !validBindingPolicyConfigurationStamp(inventory) {
		return nil, bindingPolicySnapshotFailure("selection_unconfirmed", "A complete inventory, exact live deployment and valid configuration stamp are required.", nil)
	}
	app, err := client.GetApp(ctx, slug)
	appID, parseErr := uuid.Parse(app.ID)
	if err != nil || parseErr != nil || appID == uuid.Nil || app.Slug != slug {
		return nil, bindingPolicySnapshotFailure("app_unconfirmed", "Could not confirm the app identity.", err)
	}
	policy, err := client.GetBindingReleasePolicy(ctx, slug, inventory.VerificationScope)
	if err != nil {
		return nil, bindingPolicySnapshotFailure("release_policy_unavailable", "Could not read the stored release policy; no fallback was used.", err)
	}
	if err := validateReleasePolicyCheckTarget(policy, appID.String(), inventory.VerificationScope); err != nil {
		return nil, bindingPolicySnapshotFailure("release_policy_invalid", "The stored policy has invalid fields or belongs to another app or scope.", err)
	}
	// Even implicit selection must be reconfirmed by a fresh explicit request
	// before any task. Older servers ignoring deployment_id fail closed.
	inventory.Scope, inventory.RequestedDeploymentID = policy.Scope, inventory.VerificationDeploymentID
	snapshot := &bindingPolicySnapshotClient{Client: client, slug: slug, inventory: inventory, policy: copyBindingReleasePolicy(policy)}
	snapshot.outboundIDs, snapshot.outboundConfiguration, err = snapshot.readOutboundConfiguration(ctx)
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func validBindingPolicyConfigurationStamp(i api.AppBindingInventory) bool {
	r := i.RuntimeFreshness
	return i.Complete && len(i.Issues) == 0 && r != nil && r.Source == "instance_started_at" && !r.ObservedAt.IsZero() && !r.ObservedAt.After(time.Now()) &&
		r.ConfigChangedAt != nil && !r.ConfigChangedAt.IsZero() && !r.ConfigChangedAt.After(r.ObservedAt)
}

func (s *bindingPolicySnapshotClient) GetApp(ctx context.Context, slug string) (api.AppResponse, error) {
	app, err := s.Client.GetApp(ctx, slug)
	if err != nil || slug != s.slug || app.Slug != s.slug || !sameBindingDeployment(app.ID, s.policy.AppID) {
		return app, bindingPolicySnapshotFailure("app_changed", "The app identity could not be reconfirmed.", err)
	}
	return app, nil
}

func (s *bindingPolicySnapshotClient) GetBindingReleasePolicy(ctx context.Context, slug, scope string) (api.BindingReleasePolicy, error) {
	p, err := s.Client.GetBindingReleasePolicy(ctx, slug, scope)
	if err != nil {
		return p, bindingPolicySnapshotFailure("release_policy_unavailable", "Could not reconfirm the stored release policy.", err)
	}
	if slug != s.slug || scope != s.policy.Scope || validateReleasePolicyCheckTarget(p, s.policy.AppID, scope) != nil || !sameBindingReleasePolicy(s.policy, p) {
		return p, bindingPolicySnapshotFailure("release_policy_changed", "The release policy changed; preview and verify the new policy before continuing.", nil)
	}
	return p, nil
}

func (s *bindingPolicySnapshotClient) GetAppBindingInventoryForDeployment(ctx context.Context, slug, scope, deployment string) (api.AppBindingInventory, error) {
	i, err := s.GetAppBindingInventoryForServiceTargets(ctx, slug, scope, deployment, s.inventory.ServiceTargets)
	if err != nil {
		return i, bindingPolicySnapshotFailure("inventory_unavailable", "Could not reconfirm the binding inventory.", err)
	}
	local := bindingcheck.Policy{App: s.slug, Scope: s.policy.Scope, DeploymentID: s.inventory.VerificationDeploymentID}
	if slug != s.slug || scope != local.Scope || !sameBindingDeployment(deployment, local.DeploymentID) || !validReleasePolicyCheckSelection(i, local) || !validBindingPolicyConfigurationStamp(i) {
		return i, bindingPolicySnapshotFailure("selection_unconfirmed", "The server did not reconfirm a complete inventory for the original deployment and scope.", nil)
	}
	if !validBindingPolicyServiceTargets(i, s.inventory.ServiceTargets) {
		return i, bindingPolicySnapshotFailure("service_targets_unconfirmed", "The server did not reconfirm the proposed service graph.", nil)
	}
	if !i.RuntimeFreshness.ConfigChangedAt.Equal(*s.inventory.RuntimeFreshness.ConfigChangedAt) || bindingPolicyBindingConfiguration(i, s.policy) != bindingPolicyBindingConfiguration(s.inventory, s.policy) {
		return i, bindingPolicySnapshotFailure("binding_configuration_changed", "Binding configuration changed; preview and verify the new configuration before continuing.", nil)
	}
	ids, configuration, err := s.readOutboundConfiguration(ctx)
	if err != nil {
		return i, err
	}
	if !maps.Equal(ids, s.outboundIDs) || configuration != s.outboundConfiguration {
		return i, bindingPolicySnapshotFailure("binding_configuration_changed", "Outbound binding identity or routing configuration changed; preview and verify the new configuration before continuing.", nil)
	}
	return i, nil
}

func bindingPolicyBindingConfiguration(i api.AppBindingInventory, p api.BindingReleasePolicy) string {
	return bindingcheck.VerificationConfiguration(i, p)
}

func sortedBindingPolicyStrings(values []string) []string {
	copy := slices.Clone(values)
	slices.Sort(copy)
	return copy
}

func (s *bindingPolicySnapshotClient) confirm(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.GetApp(ctx, s.slug); err != nil {
		return err
	}
	if _, err := s.GetAppBindingInventoryForDeployment(ctx, s.slug, s.policy.Scope, s.inventory.VerificationDeploymentID); err != nil {
		return err
	}
	if _, err := s.GetBindingReleasePolicy(ctx, s.slug, s.policy.Scope); err != nil {
		return err
	}
	return ctx.Err()
}

type bindingPolicyProbeClient struct {
	*deploymentBindingProbeClient
	snapshot              *bindingPolicySnapshotClient
	ownedTaskID           string
	ownedTaskFinished     bool
	cancellationRequested bool
	commands              map[string][]string
}

func (c *bindingPolicyProbeClient) CreateAppTask(ctx context.Context, slug string, request api.CreateAppTaskRequest) (api.AppTaskResponse, error) {
	if err := c.snapshot.confirm(ctx); err != nil {
		return api.AppTaskResponse{}, err
	}
	pinManagedServiceRequest(c.snapshot.inventory, &request)
	if len(request.Command) == 2 && request.Command[0] == api.AppTaskServiceBindingProbeCommand {
		if target := c.snapshot.inventory.ServiceTargets[request.Command[1]]; target != "" {
			request.Command = []string{api.AppTaskServiceBindingTargetProbeCommand, request.Command[1], target}
		}
	}
	var task api.AppTaskResponse
	var err error
	if ctx.Value(bindingVerificationRunKey{}) != nil {
		step, admitErr := admitVerificationRunStep(ctx, c.Client, slug, "task")
		if admitErr != nil {
			return task, admitErr
		}
		task, err = c.deploymentBindingProbeClient.GetAppTask(ctx, slug, step.ResourceID)
		if err == nil && sameBindingDeployment(task.ID, step.ResourceID) && len(request.Command) == 2 && request.Command[0] == api.AppTaskOutboundBindingProbeCommand && len(task.Command) == 3 && slices.Equal(task.Command[:2], request.Command) {
			task.Command = slices.Clone(request.Command)
		}
		if err == nil && !sameBindingDeployment(task.ID, step.ResourceID) {
			return task, errors.New("run task receipt identity changed")
		}
	} else {
		task, err = c.deploymentBindingProbeClient.CreateAppTask(ctx, slug, request)
	}
	if id, parseErr := uuid.Parse(task.ID); err == nil && parseErr == nil && id != uuid.Nil {
		c.ownedTaskID = task.ID
		c.ownedTaskFinished = task.Status.Terminal()
	}
	if err == nil && (!c.validTaskIdentity(task) || !slices.Equal(task.Command, request.Command) || task.CommandShell) {
		id, parseErr := uuid.Parse(task.ID)
		if bindingProbeAutoCancel(ctx) && parseErr == nil && id != uuid.Nil {
			cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bindingProbeCancelTimeout)
			defer cancel()
			if _, cancelErr := c.CancelAppTask(cancelCtx, slug, task.ID); cancelErr != nil {
				PrintWarn(osStderr, "could not cancel unconfirmed binding policy canary task %s: %v", task.ID, cancelErr)
			}
		}
		return task, bindingPolicySnapshotFailure("task_unconfirmed", "The server did not confirm the admitted canary identity and command.", nil)
	}
	if err == nil {
		if c.commands == nil {
			c.commands = map[string][]string{}
		}
		c.commands[task.ID] = slices.Clone(request.Command)
	}
	return task, err
}

func (c *bindingPolicyProbeClient) validTaskIdentity(task api.AppTaskResponse) bool {
	id, err := uuid.Parse(task.ID)
	return err == nil && id != uuid.Nil && sameBindingDeployment(task.AppID, c.snapshot.policy.AppID)
}

func (c *bindingPolicyProbeClient) GetAppTask(ctx context.Context, slug, taskID string) (api.AppTaskResponse, error) {
	task, err := c.deploymentBindingProbeClient.GetAppTask(ctx, slug, taskID)
	if ctx.Value(bindingVerificationRunKey{}) != nil && err == nil && len(task.Command) == 3 && task.Command[0] == api.AppTaskOutboundBindingProbeCommand && slices.Equal(task.Command[:2], c.commands[taskID]) {
		task.Command = slices.Clone(c.commands[taskID])
	}
	if bindingProbeAutoCancel(ctx) && err != nil && errors.Is(ctx.Err(), context.Canceled) {
		// An interrupt can arrive during the HTTP read, before the executor's
		// timer select can observe it. Use the original admitted ID to cancel.
		cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bindingProbeCancelTimeout)
		defer cancel()
		if _, cancelErr := c.CancelAppTask(cancelCtx, slug, taskID); cancelErr != nil {
			PrintWarn(osStderr, "could not cancel interrupted binding policy canary task %s: %v", taskID, cancelErr)
		}
		return task, fmt.Errorf("binding policy canary interrupted during status read: %w", ctx.Err())
	}
	if err == nil && (!c.validTaskIdentity(task) || !sameBindingDeployment(task.ID, taskID) || task.CommandShell || !slices.Equal(task.Command, c.commands[taskID])) {
		return task, bindingPolicySnapshotFailure("task_unconfirmed", "The canary receipt does not match the admitted task, app or command.", nil)
	}
	if err == nil {
		c.ownedTaskFinished = task.Status.Terminal()
	}
	return task, err
}

func validBindingPolicyServiceTargets(inventory api.AppBindingInventory, targets map[string]string) bool {
	if !maps.Equal(inventory.ServiceTargets, targets) {
		return false
	}
	seen := map[string]bool{}
	for _, item := range inventory.Bindings {
		if item.Type == api.BindingTypeService {
			if item.ServiceContext != nil {
				if _, supported := api.ManagedServiceProbeForItem(item); !supported || targets[item.Name] != "" {
					return false
				}
				continue
			}
			if item.ServiceTargetDeploymentID != targets[item.Name] {
				return false
			}
			seen[item.Name] = true
		}
	}
	for service := range targets {
		if !seen[service] {
			return false
		}
	}
	return true
}

func (c *bindingPolicyProbeClient) CancelAppTask(ctx context.Context, slug, id string) (api.AppTaskResponse, error) {
	task, err := c.deploymentBindingProbeClient.CancelAppTask(ctx, slug, id)
	if err == nil && id == c.ownedTaskID {
		c.cancellationRequested = true
	}
	return task, err
}
