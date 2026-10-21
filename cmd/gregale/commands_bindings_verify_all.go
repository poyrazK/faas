package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type allBindingProbeOptions struct {
	client             *api.Client
	publish            bool
	requireComplete    bool
	objectStorageWrite bool
	dryRun             bool
}

func runAllBindingProbes(ctx context.Context, client bindingInventoryProbeClient, slug string, pollInterval, waitTimeout time.Duration, probeOptions ...allBindingProbeOptions) int {
	var options allBindingProbeOptions
	if len(probeOptions) > 0 {
		options = probeOptions[0]
	}
	interruptedContext, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	ctx = interruptedContext
	inventory, err := client.GetAppBindingInventory(ctx, slug, "")
	if err != nil {
		return printErr("Could not load app bindings", err)
	}
	if inventory.ServiceBindingSource == "environment_release" {
		client = &managedInventoryProbeClient{bindingInventoryProbeClient: client, inventory: inventory, slug: slug}
	}
	selection := prepareAllBindingProbes(ctx, client, slug, inventory, options)
	if options.dryRun {
		return previewAllBindingProbes(inventory, selection, options)
	}
	selected, omitted := selection.selected, selection.omitted
	batch := selection.batch
	hasReadErrors, unknownType := selection.hasReadErrors, selection.unknownType
	outboundIDsByName := selection.outboundIDsByName
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
		case api.BindingTypeQueue:
			queueContext := context.WithValue(batchContext, queueCanaryScopeKey{}, batch.Scope)
			report, queueErr := executeQueueBindingCanary(queueContext, options.client, slug, binding.Binding, batch.DeploymentID, options.publish, pollInterval, "")
			item.QueueReport = &report
			if queueErr != nil {
				code = 1
				item.Reason = "queue_canary_failed"
				item.Error = redactErrorText(queueErr.Error())
				if errors.Is(queueErr, context.Canceled) {
					code = 130
				}
			}
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
			mode := allBindingStorageProbeMode(binding, options)
			report, actualTask, title, exit, probeErr := executeObjectStorageBindingProbeMode(batchContext, client, slug, binding.Binding, mode, pollInterval, waitTimeout)
			task = actualTask
			item.ObjectStorageReport, code = &report, exit
			if title != "" {
				report.Error = probeErr.Error()
			}
		}
		if task.DeploymentScope != "" {
			item.Scope = task.DeploymentScope
		}
		if binding.Type != api.BindingTypeQueue && code == 0 && bindingProbeDeploymentChanged(inventory, task) {
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
	if options.requireComplete {
		batch.Gate = evaluateAllBindingCoverageGate(batch, inventory.Complete)
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
	if batch.Failed > 0 || batch.NotChecked > 0 || hasReadErrors || unknownType || len(selected) == 0 || batch.Gate != nil && !batch.Gate.Passed {
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
		if api.ServiceBindingVerificationUnsupported(item) {
			return "unsupported", api.ManagedServiceVerificationUnsupported
		}
		return "", ""
	case api.BindingTypePostgres, api.BindingTypeObjectStorage:
		if item.Scope != scope {
			return "skipped", "outside_deployment_scope"
		}
		if item.Type == api.BindingTypePostgres && item.Access == "migration" {
			return "skipped", "release_only"
		}
		if item.Type == api.BindingTypeObjectStorage && item.Access == "write" {
			return "unsupported", "write_probe_requires_opt_in"
		}
		return "", ""
	case api.BindingTypeQueue:
		if !api.QueueBindingInScope(item, scope) {
			return "skipped", "outside_deployment_scope"
		}
		return "unsupported", "queue_publish_consent_required"
	case api.BindingTypeOutbound:
		if item.OutboundProbe == nil {
			return "unsupported", "outbound_probe_unconfigured"
		}
		return "", ""
	default:
		return "unsupported", "unknown_binding_type"
	}
}

func allBindingQueueOmission(item api.AppBindingInventoryItem, inventory api.AppBindingInventory, options allBindingProbeOptions) (string, string) {
	if item.Access != "push" {
		return "unsupported", "queue_delivery_unsupported"
	}
	if !item.QueueCanarySupported {
		if item.QueueCanaryReason != "" {
			return "unsupported", item.QueueCanaryReason
		}
		return "unsupported", "queue_handler_unsupported"
	}
	if item.ConsumerState != "active" {
		return "unsupported", "queue_consumer_not_ready"
	}
	if !options.publish {
		return "unsupported", "queue_publish_consent_required"
	}
	if options.client == nil || inventory.RequestedDeploymentID == "" || !sameBindingDeployment(inventory.RequestedDeploymentID, inventory.VerificationDeploymentID) {
		return "unsupported", "queue_deployment_required"
	}
	if !api.ValidQueueBindingName(item.Binding) {
		return "unsupported", "queue_binding_invalid"
	}
	return "", ""
}

// A write opt-in never infers write permission from missing or future metadata.
func allBindingStorageWriteOmission(item api.AppBindingInventoryItem, inventory api.AppBindingInventory) (string, string) {
	if !api.ValidObjectStorageBindingPrefix(item.Binding) {
		return "unsupported", "object_storage_binding_invalid"
	}
	switch item.Access {
	case "read":
		return "", ""
	case "write", "read_write":
		if inventory.RequestedDeploymentID == "" || !sameBindingDeployment(inventory.RequestedDeploymentID, inventory.VerificationDeploymentID) {
			return "unsupported", "object_storage_deployment_required"
		}
		return "", ""
	default:
		return "unsupported", "object_storage_access_unsupported"
	}
}

func allBindingStorageProbeMode(binding api.AppBindingInventoryItem, options allBindingProbeOptions) string {
	if options.objectStorageWrite && api.ValidObjectStorageWriteProbeMode(binding.Access) {
		return binding.Access
	}
	return ""
}
