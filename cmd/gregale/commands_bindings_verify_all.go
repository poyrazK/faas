package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func runAllBindingProbes(ctx context.Context, client bindingInventoryProbeClient, slug string, pollInterval, waitTimeout time.Duration) int {
	inventory, err := client.GetAppBindingInventory(ctx, slug, "")
	if err != nil {
		return printErr("Could not load app bindings", err)
	}
	selected := []api.AppBindingInventoryItem{}
	for _, item := range inventory.Bindings {
		if item.Type == api.BindingTypeOutbound && item.OutboundProbe != nil && item.State != "disabled" || item.Type == api.BindingTypeService || (item.Type == api.BindingTypePostgres || item.Type == api.BindingTypeObjectStorage) && item.Scope == inventory.VerificationScope {
			selected = append(selected, item)
		}
	}
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].Type+selected[i].Name+selected[i].Binding < selected[j].Type+selected[j].Name+selected[j].Binding
	})
	batch := serviceBindingProbeBatchReport{App: slug, DeploymentID: inventory.VerificationDeploymentID, Scope: inventory.VerificationScope, Total: len(selected), Bindings: []serviceBindingProbeBatchItem{}}
	hasReadErrors := false
	for _, issue := range inventory.Issues {
		if issue.Type != api.BindingTypePostgres && issue.Type != api.BindingTypeObjectStorage && issue.Type != "verification" && issue.Type != api.BindingTypeOutbound {
			continue
		}
		batch.Issues = append(batch.Issues, issue)
		hasReadErrors = hasReadErrors || issue.Severity == "error"
	}
	if len(selected) == 0 && !hasReadErrors {
		return printErr("No supported bindings to verify", fmt.Errorf("%s has no supported configured bindings", slug))
	}
	outboundIDsByName := map[string]string{}
	needsOutbound := false
	for _, item := range selected {
		needsOutbound = needsOutbound || item.Type == api.BindingTypeOutbound
	}
	if needsOutbound {
		bindings, err := client.ListOutboundAppBindings(ctx, slug)
		if err != nil {
			return printErr("Could not list outbound bindings", err)
		}
		for _, binding := range bindings.Items {
			outboundIDsByName[binding.Integration.Name] = binding.Integration.ID
		}
	}
	batchContext, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	interrupted := false
	for _, binding := range selected {
		item := serviceBindingProbeBatchItem{Type: binding.Type, Name: binding.Name, Binding: binding.Binding, Scope: batch.Scope, Status: "not_checked"}
		if binding.Type == api.BindingTypeService {
			item.Service = binding.Name
		}
		var code int
		var task api.AppTaskResponse
		if interrupted || batchContext.Err() != nil {
			batch.Skipped++
			batch.Bindings = append(batch.Bindings, item)
			continue
		}
		switch binding.Type {
		case api.BindingTypeService:
			report, actualTask, title, exit, probeErr := executeServiceBindingProbe(batchContext, client, slug, binding.Name, pollInterval, waitTimeout)
			task = actualTask
			item.Service, item.Report, code = binding.Name, &report, exit
			if title != "" {
				report.Error = probeErr.Error()
			}
		case api.BindingTypePostgres:
			report, actualTask, title, exit, probeErr := executePostgresBindingProbe(batchContext, client, slug, binding.Binding, pollInterval, waitTimeout)
			task = actualTask
			item.PostgresReport, code = &report, exit
			if title != "" {
				report.Error = probeErr.Error()
			}
		case api.BindingTypeOutbound:
			integrationID := outboundIDsByName[binding.Name]
			if integrationID == "" {
				batch.Skipped++
				item.Status, item.Name = "skipped", binding.Name
				batch.Bindings = append(batch.Bindings, item)
				continue
			}
			report, actualTask, title, exit, probeErr := executeOutboundBindingProbe(batchContext, client, slug, integrationID, pollInterval, waitTimeout)
			task = actualTask
			item.OutboundReport, code = &report, exit
			if title != "" {
				report.Error = probeErr.Error()
			}
		case api.BindingTypeObjectStorage:
			report, actualTask, title, exit, probeErr := executeObjectStorageBindingProbe(batchContext, client, slug, binding.Binding, pollInterval, waitTimeout)
			task = actualTask
			item.ObjectStorageReport, code = &report, exit
			if title != "" {
				report.Error = probeErr.Error()
			}
		}
		if task.DeploymentScope != "" {
			item.Scope = task.DeploymentScope
		}
		if code == 0 && bindingProbeDeploymentChanged(inventory, task) {
			if item.Report != nil {
				item.Report.Error = "live deployment changed during verification; run verification again"
			} else if item.PostgresReport != nil {
				item.PostgresReport.Error = "live deployment changed during verification; run verification again"
			} else if item.OutboundReport != nil {
				item.OutboundReport.Error = "live deployment changed during verification; run verification again"
			} else {
				item.ObjectStorageReport.Error = "live deployment changed during verification; run verification again"
			}
			code = 1
		}
		switch code {
		case 0:
			item.Status = "passed"
			batch.Passed++
			batch.Checked++
		case 130:
			interrupted = true
			batch.Skipped++
		default:
			item.Status = "failed"
			batch.Failed++
			batch.Checked++
		}
		batch.Bindings = append(batch.Bindings, item)
	}
	if jsonOutput {
		if err := writeJSON(batch); err != nil {
			return jsonOut(err)
		}
	} else {
		renderServiceBindingProbeBatch(batch)
	}
	if interrupted {
		return 130
	}
	if batch.Failed > 0 || batch.Skipped > 0 || hasReadErrors {
		return 1
	}
	return 0
}
