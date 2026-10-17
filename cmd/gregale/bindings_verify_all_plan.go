package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// Preparation only reads metadata. Both preview and execution consume this
// selection, including the outbound identities resolved for selected probes.
type allBindingProbeSelection struct {
	selected          []api.AppBindingInventoryItem
	omitted           []serviceBindingProbeBatchItem
	batch             serviceBindingProbeBatchReport
	hasReadErrors     bool
	unknownType       bool
	outboundIDsByName map[string]string
}

func prepareAllBindingProbes(ctx context.Context, client bindingInventoryProbeClient, slug string, inventory api.AppBindingInventory, options allBindingProbeOptions) allBindingProbeSelection {
	selected := []api.AppBindingInventoryItem{}
	omitted := []serviceBindingProbeBatchItem{}
	batch := serviceBindingProbeBatchReport{App: slug, DeploymentID: inventory.VerificationDeploymentID, Scope: inventory.VerificationScope, Total: len(inventory.Bindings), Coverage: "complete", Bindings: []serviceBindingProbeBatchItem{}, Issues: append([]api.BindingInventoryIssue{}, inventory.Issues...)}
	hasReadErrors := !inventory.Complete
	unknownType := false
	for _, issue := range inventory.Issues {
		hasReadErrors = hasReadErrors || issue.Severity != "warning"
	}
	queueNames := map[string]int{}
	storagePrefixes := map[string]int{}
	for _, item := range inventory.Bindings {
		if item.Type == api.BindingTypeObjectStorage && item.State != "disabled" && item.Scope == inventory.VerificationScope {
			storagePrefixes[item.Binding]++
		}
		if item.Type == api.BindingTypeQueue && item.State != "disabled" && api.QueueBindingInScope(item, inventory.VerificationScope) {
			queueNames[item.Binding]++
		}
	}
	for _, item := range inventory.Bindings {
		status, reason := bindingProbeOmission(item, inventory.VerificationScope)
		if item.Type == api.BindingTypeQueue && reason != "binding_disabled" && reason != "outside_deployment_scope" {
			status, reason = allBindingQueueOmission(item, inventory, options)
			if queueNames[item.Binding] > 1 {
				status, reason = "unsupported", "queue_binding_ambiguous"
			}
		}
		if options.objectStorageWrite && item.Type == api.BindingTypeObjectStorage && reason != "binding_disabled" && reason != "outside_deployment_scope" {
			status, reason = allBindingStorageWriteOmission(item, inventory)
			if storagePrefixes[item.Binding] > 1 {
				status, reason = "unsupported", "object_storage_binding_ambiguous"
			}
		}
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
	return allBindingProbeSelection{selected: selected, omitted: omitted, batch: batch, hasReadErrors: hasReadErrors, unknownType: unknownType, outboundIDsByName: outboundIDsByName}
}

type allBindingPreviewConsent struct {
	Flag     string `json:"flag"`
	Provided bool   `json:"provided"`
}

type allBindingPreviewStep struct {
	Type           string                    `json:"type"`
	Name           string                    `json:"name"`
	Binding        string                    `json:"binding"`
	Scope          string                    `json:"scope"`
	Status         string                    `json:"status"`
	Action         string                    `json:"action"`
	ProbeMode      string                    `json:"probe_mode,omitempty"`
	Operations     []string                  `json:"operations"`
	Reason         string                    `json:"reason,omitempty"`
	Consent        *allBindingPreviewConsent `json:"consent,omitempty"`
	Method         string                    `json:"method,omitempty"`
	Path           string                    `json:"path,omitempty"`
	ExpectedStatus int                       `json:"expected_status,omitempty"`
}

type allBindingProbePreview struct {
	App                      string                      `json:"app"`
	DeploymentID             string                      `json:"deployment_id,omitempty"`
	Scope                    string                      `json:"scope"`
	DryRun                   bool                        `json:"dry_run"`
	PlanReady                bool                        `json:"plan_ready"`
	RequireComplete          bool                        `json:"require_complete"`
	PlannedCoverage          string                      `json:"planned_coverage"`
	CompleteCoveragePossible bool                        `json:"complete_coverage_possible"`
	Total                    int                         `json:"total"`
	Planned                  int                         `json:"planned"`
	Blocked                  int                         `json:"blocked"`
	Excluded                 int                         `json:"excluded"`
	Plan                     []allBindingPreviewStep     `json:"plan"`
	CoverageBlockers         []allBindingCoverageBlocker `json:"coverage_blockers"`
	Issues                   []api.BindingInventoryIssue `json:"issues,omitempty"`
	Note                     string                      `json:"note"`
}

func allBindingPreviewOperation(binding api.AppBindingInventoryItem, scope, status, reason string, options allBindingProbeOptions) allBindingPreviewStep {
	step := allBindingPreviewStep{Type: binding.Type, Name: binding.Name, Binding: binding.Binding, Scope: scope, Status: status, Reason: reason, Action: "none", Operations: []string{}}
	if status == "excluded" || status == "blocked" && reason != "queue_publish_consent_required" && reason != "write_probe_requires_opt_in" {
		return step
	}
	step.Action = "task-probe"
	switch binding.Type {
	case api.BindingTypeService:
		step.ProbeMode = "https_route"
		step.Operations = []string{"dns", "tls", "authorization", "routing"}
	case api.BindingTypePostgres:
		step.ProbeMode = "query"
		step.Operations = []string{"connect", "query"}
	case api.BindingTypeObjectStorage:
		step.ProbeMode = allBindingStorageProbeMode(binding, options)
		if reason == "write_probe_requires_opt_in" {
			step.ProbeMode = "write"
		}
		switch step.ProbeMode {
		case "write":
			step.Operations = []string{"upload", "cleanup"}
		case "read_write":
			step.Operations = []string{"upload", "download", "cleanup"}
		default:
			step.ProbeMode = "read"
			step.Operations = []string{"list"}
		}
		if step.ProbeMode != "read" {
			step.Consent = &allBindingPreviewConsent{Flag: "--object-storage-write", Provided: options.objectStorageWrite}
		}
	case api.BindingTypeOutbound:
		step.ProbeMode = "configured_request"
		step.Operations = []string{"request"}
		if binding.OutboundProbe != nil && binding.OutboundProbe.Valid() {
			step.Method = binding.OutboundProbe.Method
			step.Path = binding.OutboundProbe.Path
			step.ExpectedStatus = binding.OutboundProbe.ExpectedStatus
		}
	case api.BindingTypeQueue:
		step.Action = "queue-canary"
		step.ProbeMode = api.QueueEvidenceHandlerCompletion
		step.Operations = []string{"publish", "handler_completion"}
		step.Consent = &allBindingPreviewConsent{Flag: "--queue-publish", Provided: options.publish}
	}
	return step
}

func buildAllBindingProbePreview(inventory api.AppBindingInventory, selection allBindingProbeSelection, options allBindingProbeOptions) allBindingProbePreview {
	preview := allBindingProbePreview{App: selection.batch.App, DeploymentID: selection.batch.DeploymentID, Scope: selection.batch.Scope, DryRun: true, RequireComplete: options.requireComplete, PlannedCoverage: "complete", Total: selection.batch.Total, Plan: []allBindingPreviewStep{}, Issues: selection.batch.Issues, Note: "Read-only metadata preview. Plan readiness assumes server admission and successful probes; runtime verification is still required. Preview does not reserve or bind a later execution."}
	projection := selection.batch
	projection.Bindings = make([]serviceBindingProbeBatchItem, 0, len(inventory.Bindings))
	identityMissing := false
	for _, binding := range selection.selected {
		status, reason := "planned", ""
		projectionStatus := "planned"
		if binding.Type == api.BindingTypeOutbound && selection.outboundIDsByName[binding.Name] == "" {
			status, reason, projectionStatus = "blocked", "outbound_identity_unavailable", "not_checked"
			identityMissing = true
		}
		preview.Plan = append(preview.Plan, allBindingPreviewOperation(binding, selection.batch.Scope, status, reason, options))
		projection.Bindings = append(projection.Bindings, serviceBindingProbeBatchItem{Type: binding.Type, Name: binding.Name, Binding: binding.Binding, Scope: selection.batch.Scope, Status: projectionStatus, Reason: reason})
		if status == "planned" {
			preview.Planned++
		} else {
			preview.Blocked++
		}
	}
	metadata := map[string]api.AppBindingInventoryItem{}
	for _, binding := range inventory.Bindings {
		metadata[bindingProbeSortKey(binding)] = binding
	}
	for _, omitted := range selection.omitted {
		status := "blocked"
		if omitted.Status == "skipped" {
			status = "excluded"
			preview.Excluded++
		} else {
			preview.Blocked++
		}
		key := bindingProbeSortKey(api.AppBindingInventoryItem{Type: omitted.Type, Name: omitted.Name, Binding: omitted.Binding, Scope: omitted.Scope})
		preview.Plan = append(preview.Plan, allBindingPreviewOperation(metadata[key], omitted.Scope, status, omitted.Reason, options))
		projection.Bindings = append(projection.Bindings, omitted)
	}
	// Potential coverage uses planned steps as its only successful state. It
	// never turns a planned step into a passed verification result.
	potential := evaluateAllBindingCoverage(projection, inventory.Complete, "planned")
	preview.CompleteCoveragePossible = potential.Passed
	preview.CoverageBlockers = potential.Blockers
	if preview.Blocked > 0 || selection.hasReadErrors {
		preview.PlannedCoverage = "partial"
	}
	if preview.Planned == 0 {
		preview.PlannedCoverage = "none"
	}
	preview.PlanReady = preview.Planned > 0 && !selection.hasReadErrors && !selection.unknownType && !identityMissing
	if options.requireComplete && !preview.CompleteCoveragePossible {
		preview.PlanReady = false
	}
	return preview
}

func previewAllBindingProbes(inventory api.AppBindingInventory, selection allBindingProbeSelection, options allBindingProbeOptions) int {
	preview := buildAllBindingProbePreview(inventory, selection, options)
	if jsonOutput {
		if err := writeJSON(preview); err != nil {
			return jsonOut(err)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "Binding verification preview: %s scope=%s deployment=%s (read-only)\n", preview.App, preview.Scope, preview.DeploymentID)
		_, _ = fmt.Fprintf(osStdout, "Plan ready: %t; planned coverage: %s; complete coverage possible: %t\n", preview.PlanReady, preview.PlannedCoverage, preview.CompleteCoveragePossible)
		for _, step := range preview.Plan {
			_, _ = fmt.Fprintf(osStdout, "  %s %s (%s) scope=%s: %s action=%s mode=%s operations=%v\n", step.Type, step.Name, step.Binding, step.Scope, step.Status, step.Action, step.ProbeMode, step.Operations)
			if step.Reason != "" {
				_, _ = fmt.Fprintf(osStdout, "    %s\n", step.Reason)
			}
			if step.Consent != nil {
				_, _ = fmt.Fprintf(osStdout, "    consent: %s provided=%t\n", step.Consent.Flag, step.Consent.Provided)
			}
			if step.Method != "" {
				_, _ = fmt.Fprintf(osStdout, "    request: %s %s (expected status %d)\n", step.Method, step.Path, step.ExpectedStatus)
			}
		}
		for _, blocker := range preview.CoverageBlockers {
			_, _ = fmt.Fprintf(osStdout, "  coverage blocker: %s type=%s name=%s binding=%s scope=%s reason=%s\n", blocker.Code, blocker.Type, blocker.Name, blocker.Binding, blocker.Scope, blocker.Reason)
		}
		_, _ = fmt.Fprintln(osStdout, preview.Note)
	}
	if !preview.PlanReady {
		return 1
	}
	return 0
}
