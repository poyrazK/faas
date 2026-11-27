package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runpipeline"
	"io"
)

func cmdPipelineManifest(path string, managed, dryRun bool) (bool, int) {
	f, err := openWorkflowPlanFile(path)
	if err != nil {
		return true, printErr("Could not read workflow manifest", err)
	}
	defer f.Close() //nolint:errcheck
	raw, err := io.ReadAll(io.LimitReader(f, api.ExecutionWorkflowManagedPlanMaxBytes+1))
	if err != nil || len(raw) > api.ExecutionWorkflowManagedPlanMaxBytes {
		return true, printErr("Could not read workflow manifest", fmt.Errorf("manifest read failed or exceeds size limit"))
	}
	var request api.CreateManagedExecutionWorkflowRequest
	if json.Unmarshal(raw, &request) != nil || !runpipeline.Requested(request) {
		return false, 0
	}
	if !managed {
		return true, printErr("Managed mode required", fmt.Errorf("task, batch and retry manifests require --managed"))
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&request); err != nil {
		return true, printErr("Invalid pipeline manifest", err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return true, printErr("Invalid pipeline manifest", fmt.Errorf("unexpected trailing JSON"))
	}
	normalized, err := runpipeline.Normalize(request)
	if err != nil {
		return true, printErr("Invalid pipeline manifest", err)
	}
	shape := normalized
	shape.Steps = append([]api.CreateManagedExecutionWorkflowStep(nil), normalized.Steps...)
	for i := range shape.Steps {
		shape.Steps[i].ArtifactInputs = nil
	}
	if err = api.ValidateCreateManagedExecutionWorkflow(shape); err != nil {
		return true, printErr("Invalid pipeline manifest", err)
	}
	if dryRun {
		if jsonOutput {
			return true, jsonOut(writeJSON(map[string]any{"dry_run": true, "workflow_id": request.WorkflowID, "step_count": len(normalized.Steps), "catalog_validated": false}))
		}
		PrintOK(osStdout, "Pipeline %s: %d steps; manifest shape validated. Catalog, contracts and budgets are checked at submission and admission.", request.WorkflowID, len(normalized.Steps))
		return true, 0
	}
	client, err := authedClient()
	if err != nil {
		return true, printErr("Not logged in", err)
	}
	out, err := client.CreateManagedExecutionWorkflow(context.Background(), request)
	if err != nil {
		return true, printErr("Pipeline submission failed", err)
	}
	if jsonOutput {
		return true, jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Pipeline %s accepted (plan %s, status=%s, steps=%d).", out.WorkflowID, out.PlanID, out.Status, out.StepCount)
	return true, 0
}
func cmdRunsWorkflowCancel(args []string) int {
	if len(args) != 1 {
		PrintUsage(osStderr, "usage: gregale sandbox workflow cancel <workflow-id> [--json]", "runs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.CancelExecutionPipeline(context.Background(), args[0])
	if err != nil {
		return printErr("Pipeline cancellation failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Pipeline %s cancellation requested.", out.WorkflowID)
	return 0
}
