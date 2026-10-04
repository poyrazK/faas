package main

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) applyBindingRuntimeInventory(parent context.Context, r *http.Request, acct state.Account, app state.App, inventory *api.AppBindingInventory, wakeIDs map[string]string) {
	ctx, cancel := context.WithTimeout(parent, bindingInventoryReadTimeout)
	defer cancel()
	store, ok := s.store.(state.BindingRuntimeInventoryStore)
	if !ok {
		appendBindingRuntimeIssue(inventory, "runtime_freshness", "Runtime configuration freshness could not be read.")
		setBindingRefreshes(inventory, wakeIDs, nil, false)
		return
	}
	runtime, err := store.ReadBindingRuntimeInventory(ctx, acct.ID, app.ID, inventory.Scope)
	if err != nil {
		appendBindingRuntimeIssue(inventory, "runtime_freshness", "Runtime configuration freshness could not be read.")
	} else {
		inventory.RuntimeFreshness = projectBindingRuntime(runtime, time.Now().UTC())
	}
	if len(wakeIDs) == 0 {
		return
	}
	unique := make(map[string]bool)
	for _, wakeID := range wakeIDs {
		unique[wakeID] = true
	}
	ids := make([]string, 0, len(unique))
	for wakeID := range unique {
		ids = append(ids, wakeID)
	}
	sort.Strings(ids)
	rows, err := store.ListBindingRefreshInventory(ctx, acct.ID, app.ID, ids)
	setBindingRefreshes(inventory, wakeIDs, rows, err == nil)
	if err != nil {
		appendBindingRuntimeIssue(inventory, "binding_refresh", "Binding refresh progress could not be read.")
	}
}

func projectBindingRuntime(runtime state.BindingRuntimeInventory, observedAt time.Time) *api.BindingRuntimeFreshness {
	result := &api.BindingRuntimeFreshness{Source: "instance_started_at", ObservedAt: observedAt,
		ConfigChangedAt: runtime.ChangedAt, Deployments: []api.BindingRuntimeDeployment{}}
	counts := func(c state.BindingRuntimeCounts) api.BindingRuntimeInstanceCounts {
		return api.BindingRuntimeInstanceCounts{Current: c.Current, Stale: c.Stale, Unknown: c.Unknown}
	}
	for _, row := range runtime.Deployments {
		result.Deployments = append(result.Deployments, api.BindingRuntimeDeployment{
			DeploymentID: row.ID, Scope: row.Scope, DeploymentStatus: row.DeploymentStatus,
			Status: bindingRuntimeDeploymentStatus(row), Serving: counts(row.Serving), Resident: counts(row.Resident), Starting: row.Starting,
		})
	}
	return result
}

func bindingRuntimeDeploymentStatus(row state.BindingRuntimeDeployment) string {
	switch {
	case row.Resident.Stale > 0:
		return "stale"
	case row.Resident.Unknown > 0:
		return "unknown"
	case row.Starting > 0:
		return "updating"
	case row.Resident.Current > 0:
		return "current"
	default:
		return "inactive"
	}
}

func setBindingRefreshes(inventory *api.AppBindingInventory, wakeIDs map[string]string, rows []state.BindingRefreshInventory, available bool) {
	byID := make(map[string]state.BindingRefreshInventory, len(rows))
	for _, row := range rows {
		byID[row.WakeID] = row
	}
	for index := range inventory.Bindings {
		item := &inventory.Bindings[index]
		wakeID := wakeIDs[bindingVerificationKey(item.Type, item.Binding, item.Scope)]
		if wakeID == "" {
			continue
		}
		item.Refresh = &api.BindingRefresh{WakeID: wakeID, Status: "unknown"}
		if !available {
			continue
		}
		row, exists := byID[wakeID]
		if !exists {
			item.Refresh.Status = "not_queued"
			continue
		}
		item.Refresh.Status, item.Refresh.Attempts = row.Status, row.Attempts
		item.Refresh.FailureReason, item.Refresh.CompletedAt = row.FailureReason, row.CompletedAt
		if !row.RequestedAt.IsZero() {
			item.Refresh.RequestedAt = &row.RequestedAt
		}
	}
}

func appendBindingRuntimeIssue(inventory *api.AppBindingInventory, kind, message string) {
	inventory.Issues = append(inventory.Issues, api.BindingInventoryIssue{Type: kind, Code: "unavailable", Severity: "error", Message: message})
	inventory.Warnings = append(inventory.Warnings, message)
}
