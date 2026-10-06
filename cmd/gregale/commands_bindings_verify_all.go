package main

import (
	"context"
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
	omitted := []serviceBindingProbeBatchItem{}
	batch := serviceBindingProbeBatchReport{App: slug, DeploymentID: inventory.VerificationDeploymentID, Scope: inventory.VerificationScope, Total: len(inventory.Bindings), Coverage: "complete", Bindings: []serviceBindingProbeBatchItem{}, Issues: append([]api.BindingInventoryIssue{}, inventory.Issues...)}
	hasReadErrors := !inventory.Complete
	unknownType := false
	for _, issue := range inventory.Issues {
		hasReadErrors = hasReadErrors || issue.Severity != "warning"
	}
	for _, item := range inventory.Bindings {
		status, reason := bindingProbeOmission(item, inventory.VerificationScope)
		if status == "" {
			selected = append(selected, item)
			continue
		}
		omitted = append(omitted, serviceBindingProbeBatchItem{Type: item.Type, Name: item.Name, Binding: item.Binding, Scope: item.Scope, Status: status, Reason: reason})
		if status == "unsupported" {
			batch.Unsupported++
		} else {
			batch.Skipped++
		}
		unknownType = unknownType || reason == "unknown_binding_type"
	}
	sort.Slice(selected, func(i, j int) bool { return bindingProbeSortKey(selected[i]) < bindingProbeSortKey(selected[j]) })
	sort.Slice(omitted, func(i, j int) bool {
		a, b := omitted[i], omitted[j]
		return a.Type+"\x00"+a.Name+"\x00"+a.Binding+"\x00"+a.Scope < b.Type+"\x00"+b.Name+"\x00"+b.Binding+"\x00"+b.Scope
	})
	outboundIDsByName := map[string]string{}
	needsOutbound := false
	for _, item := range selected {
		needsOutbound = needsOutbound || item.Type == api.BindingTypeOutbound
	}
	if needsOutbound {
		bindings, err := client.ListOutboundAppBindings(ctx, slug)
		if err != nil {
			hasReadErrors = true
			batch.Issues = append(batch.Issues, api.BindingInventoryIssue{Type: api.BindingTypeOutbound, Code: "query_failed", Severity: "error", Message: "Outbound binding identities could not be read."})
		} else {
			for _, binding := range bindings.Items {
				outboundIDsByName[binding.Integration.Name] = binding.Integration.ID
			}
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
			batch.NotChecked++
			item.Reason = "wait_ended"
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
				batch.NotChecked++
				item.Reason = "outbound_identity_unavailable"
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
			batch.NotChecked++
			item.Reason = "interrupted"
		default:
			item.Status = "failed"
			batch.Failed++
			batch.Checked++
		}
		batch.Bindings = append(batch.Bindings, item)
	}
	batch.Bindings = append(batch.Bindings, omitted...)
	if batch.Unsupported > 0 || batch.NotChecked > 0 || hasReadErrors {
		batch.Coverage = "partial"
	}
	if batch.Checked == 0 {
		batch.Coverage = "none"
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
	if batch.Failed > 0 || batch.NotChecked > 0 || hasReadErrors || unknownType || len(selected) == 0 {
		return 1
	}
	return 0
}

func bindingProbeSortKey(item api.AppBindingInventoryItem) string {
	return item.Type + "\x00" + item.Name + "\x00" + item.Binding + "\x00" + item.Scope
}

func bindingProbeOmission(item api.AppBindingInventoryItem, scope string) (string, string) {
	if item.State == "disabled" {
		return "skipped", "binding_disabled"
	}
	switch item.Type {
	case api.BindingTypeService:
		return "", ""
	case api.BindingTypePostgres, api.BindingTypeObjectStorage:
		if item.Scope != scope {
			return "skipped", "outside_deployment_scope"
		}
		return "", ""
	case api.BindingTypeQueue:
		return "unsupported", "queue_probe_unsupported"
	case api.BindingTypeOutbound:
		if item.OutboundProbe == nil {
			return "unsupported", "outbound_probe_unconfigured"
		}
		return "", ""
	default:
		return "unsupported", "unknown_binding_type"
	}
}
