// adr: 660
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type customerOperationRecoveryReader interface {
	InspectOperationRecovery(context.Context, string, string) (api.OperationRecoveryInspection, error)
	PreviewOperationRecovery(context.Context, string, string, api.OperationRecoveryPreviewRequest) (api.OperationRecoveryPreview, error)
}

func loadCustomerOperationPreview(req *api.OperationRecoveryRequest, evidenceFile, resultFile string) error {
	if req.RecoveryID != "" || evidenceFile != "" || req.ExpectedInspectionRevision != "" {
		return fmt.Errorf("--preview records no decision; omit --recovery-id, --evidence-file and --inspection-revision")
	}
	switch req.Resolution {
	case "succeeded":
		if resultFile == "" {
			return fmt.Errorf("succeeded requires --result-file")
		}
		result, err := readCustomerOperationFile(resultFile, api.OperationSubmissionMaxBytes)
		if err != nil {
			return err
		}
		if !json.Valid(result) {
			return fmt.Errorf("result file must contain one JSON value")
		}
		req.Result = result
	case "safe_to_retry", "failed", "cancelled":
		if resultFile != "" {
			return fmt.Errorf("only succeeded accepts --result-file")
		}
	default:
		return fmt.Errorf("invalid --resolution")
	}
	return nil
}

func runCustomerOperationRecoveryRead(ctx context.Context, client customerOperationsClient, c customerOperationCommand, out io.Writer, asJSON bool) (int, error) {
	reader, ok := client.(customerOperationRecoveryReader)
	if !ok {
		return 0, fmt.Errorf("recovery inspection requires account credentials and a supported client")
	}
	if !c.preview {
		i, err := reader.InspectOperationRecovery(ctx, c.app, c.id)
		if err != nil {
			return 0, err
		}
		if asJSON {
			return 0, json.NewEncoder(out).Encode(i)
		}
		return 0, renderCustomerOperationInspection(out, i)
	}
	p, err := reader.PreviewOperationRecovery(ctx, c.app, c.id, api.OperationRecoveryPreviewRequest{ExpectedGeneration: c.recovery.ExpectedGeneration, Resolution: c.recovery.Resolution, Result: c.recovery.Result})
	if err != nil {
		return 0, err
	}
	code := 0
	if !p.Eligible {
		code = 4
	}
	if asJSON {
		return code, json.NewEncoder(out).Encode(p)
	}
	if err = renderCustomerOperationInspection(out, p.Inspection); err != nil {
		return code, err
	}
	_, err = fmt.Fprintf(out, "Preview: resolution=%s eligible=%t evidence_required=%t starts_new_execution=%t clears_artifact_references=%t\nBlockers: %s\nReused steps: %s\nReopened steps: %s\nReusable files: %s\nFiles to publish: %s\nPlatform eligibility does not establish that external effects are safe to repeat.\n", p.Resolution, p.Eligible, p.EvidenceRequired, p.StartsNewExecution, p.ClearsArtifactReferences, strings.Join(p.Blockers, ", "), strings.Join(p.ReusedSteps, ", "), strings.Join(p.ReopenedSteps, ", "), strings.Join(p.ReusableArtifactIDs, ", "), strings.Join(p.PublishArtifactIDs, ", "))
	return code, err
}

func renderCustomerOperationInspection(out io.Writer, i api.OperationRecoveryInspection) error {
	if _, err := fmt.Fprintf(out, "%s work=%s generation=%d execution=%s/%s deployment=%s release=%s\nInspection revision: %s\nRetry blockers: %s\n", i.OperationID, i.State, i.Generation, i.ExecutionKind, i.ExecutionState, i.DeploymentID, i.ReleaseID, i.InspectionRevision, strings.Join(i.RetryBlockers, ", ")); err != nil {
		return err
	}
	for _, step := range i.Steps {
		if _, err := fmt.Fprintf(out, "Step %s: %s attempt=%d confirmed=%t outcome_unknown=%t\n", step.Name, step.State, step.Attempt, step.Confirmed, step.OutcomeUnknown); err != nil {
			return err
		}
	}
	for _, artifact := range i.Artifacts {
		if _, err := fmt.Fprintf(out, "File %s (%s): %s retained=%t step=%s generation=%d attempt=%d bytes=%d expires=%s\n", artifact.ID, artifact.Name, artifact.State, artifact.Retained, artifact.WorkflowStep, artifact.Generation, artifact.Attempt, artifact.SizeBytes, artifact.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")); err != nil {
			return err
		}
	}
	return nil
}
