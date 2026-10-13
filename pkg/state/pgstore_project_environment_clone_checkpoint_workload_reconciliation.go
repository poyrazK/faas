package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// A present native journal and every present cleanup receipt must authenticate
// before any evidence enters the report. Missing steps remain blockers; missing
// predecessors for an existing receipt are conflicts, never partial success.
func reconcileCheckpointWorkloadCleanupTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, records []projectCloneWorkloadRecord, out *ProjectEnvironmentCloneCheckpointCompensationReconciliation) error {
	q := sqlc.New()
	hasCleanup, err := q.CloneCheckpointCompensationHasWorkloadCleanup(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return mapErr(err)
	}
	_, err = q.ReadCloneCheckpointWorkloadRetirement(ctx, tx, mustPgUUID(op.ID))
	if errors.Is(mapErr(err), ErrNotFound) && !hasCleanup {
		return nil
	}
	if err != nil {
		return checkpointPgCompensationDependencyError(mapErr(err))
	}
	native, err := readCheckpointWorkloadRetirementTx(ctx, tx, op)
	if err != nil {
		return checkpointPgCompensationDependencyError(err)
	}
	if native.State != "retired" || native.Plan.EnvironmentID == "" {
		if hasCleanup {
			return ErrConflict
		}
		return nil
	}
	ownership, err := verifyCloneConfigurationOwnershipTx(ctx, tx, op, native.Plan.EnvironmentID)
	if err != nil {
		return err
	}
	if ownership == "" {
		if hasCleanup {
			return ErrConflict
		}
		return nil
	}
	if native.Plan.SourceCompensationSHA256 != out.SourceCompensationSHA256 {
		return ErrConflict
	}
	out.EnvironmentID, out.OwnershipSHA256 = native.Plan.EnvironmentID, ownership
	out.NativePlanSHA256, out.NativeRetirementSHA256 = cloneCheckpointSHA(native.Plan), native.ObservationSHA256
	config, configErr := q.ReadCloneConfigurationCleanup(ctx, tx, mustPgUUID(op.ID))
	workloads, workloadErr := q.ReadCloneWorkloadCleanup(ctx, tx, mustPgUUID(op.ID))
	deployments, deploymentErr := q.ReadCloneDeploymentCleanup(ctx, tx, mustPgUUID(op.ID))
	environment, environmentErr := q.ReadCloneEnvironmentCleanup(ctx, tx, mustPgUUID(op.ID))
	steps := []struct {
		name string
		err  error
	}{
		{"configuration", configErr}, {"workload", workloadErr}, {"deployment", deploymentErr}, {"environment", environmentErr},
	}
	complete := true
	for _, step := range steps {
		if step.err == nil {
			continue
		}
		if !errors.Is(mapErr(step.err), ErrNotFound) {
			return mapErr(step.err)
		}
		complete = false
		out.Blockers = append(out.Blockers, ProjectEnvironmentCloneCheckpointCompensationBlocker{Kind: "stage_cleanup", Name: step.name, TargetID: out.EnvironmentID, Reason: step.name + "_cleanup_unverified"})
	}
	// Native replay verified the original ownership and all present transitions,
	// including their current post-state, exact predecessors and borrower absence.
	if configErr == nil {
		out.ConfigurationCleanupSHA256 = cloneConfigurationCleanupSHA(config)
	}
	if workloadErr == nil {
		out.WorkloadCleanupSHA256 = cloneWorkloadCleanupSHA(workloads)
	}
	if deploymentErr == nil {
		out.DeploymentCleanupSHA256 = cloneDeploymentCleanupSHA(deployments)
	}
	if environmentErr == nil {
		out.EnvironmentCleanupSHA256 = cloneEnvironmentCleanupSHA(environment)
	}
	if !complete {
		return nil
	}
	out.WorkloadClosureSHA256 = cloneCheckpointSHA(struct {
		OperationID, EnvironmentID, OwnershipSHA256, SourceCompensationSHA256, CaptureRetirementSHA256, NativePlanSHA256, NativeRetirementSHA256, ConfigurationCleanupSHA256, WorkloadCleanupSHA256, DeploymentCleanupSHA256, EnvironmentCleanupSHA256 string
	}{op.ID, out.EnvironmentID, ownership, out.SourceCompensationSHA256, out.CaptureRetirementSHA256, out.NativePlanSHA256, out.NativeRetirementSHA256, out.ConfigurationCleanupSHA256, out.WorkloadCleanupSHA256, out.DeploymentCleanupSHA256, out.EnvironmentCleanupSHA256})
	blockers := make([]ProjectEnvironmentCloneCheckpointCompensationBlocker, 0, len(out.Blockers))
	for _, b := range out.Blockers {
		if b.Reason == "workload_configuration_retirement_unverified" {
			continue
		}
		if b.Reason == "resource_retirement_unverified" && checkpointClosedWorkloadBlocker(op, records, b) {
			continue
		}
		blockers = append(blockers, b)
	}
	out.Blockers = blockers
	return nil
}

func cloneDeploymentCleanupSHA(row sqlc.ProjectEnvironmentCloneDeploymentCleanup) string {
	return cloneCheckpointSHA(struct {
		OperationID, EnvironmentID, WorkloadCleanupSHA256, NativePlanSHA256, NativeObservationSHA256 string
		RemovedDeployments                                                                           int64
		CleanedAt                                                                                    time.Time
	}{pgUUIDString(row.OperationID), pgUUIDString(row.EnvironmentID), row.WorkloadCleanupSha256, row.NativePlanSha256, row.NativeObservationSha256, row.RemovedDeployments, row.CleanedAt.Time.UTC()})
}

func cloneEnvironmentCleanupSHA(row sqlc.ProjectEnvironmentCloneEnvironmentCleanup) string {
	return cloneCheckpointSHA(struct {
		OperationID, EnvironmentID, WorkloadCleanupSHA256, ManifestSHA256 string
		CleanedAt                                                         time.Time
	}{pgUUIDString(row.OperationID), pgUUIDString(row.EnvironmentID), row.WorkloadCleanupSha256, row.ManifestSha256, row.CleanedAt.Time.UTC()})
}

// Check every resource behind a blocker: duplicate labels with a different
// identity/version cannot disappear because another matching label is owned.
func checkpointClosedWorkloadBlocker(op ProjectEnvironmentCloneOperation, records []projectCloneWorkloadRecord, b ProjectEnvironmentCloneCheckpointCompensationBlocker) bool {
	found := false
	for _, r := range op.Resources {
		if r.Kind != b.Kind || r.Name != b.Name || r.TargetID != b.TargetID {
			continue
		}
		if !checkpointClosedWorkloadResource(op, records, r) {
			return false
		}
		found = true
	}
	return found
}

func checkpointClosedWorkloadResource(op ProjectEnvironmentCloneOperation, records []projectCloneWorkloadRecord, r ProjectEnvironmentCloneResource) bool {
	// These identities are versions of the original frozen source capture, not
	// target objects. Neither a label nor a claimed compensated status is proof.
	if r.CapturePoint != "" {
		return false
	}
	if r.Kind == "source_revision" {
		return r.Name == op.SourceEnvironment && r.SourceVersion == op.SourceRevisionHash && r.SourceID == "" && r.TargetID == ""
	}
	if r.Kind == "project_config" {
		return len(records) > 0 && r.Name == op.SourceEnvironment && r.SourceVersion == records[0].SourceProjectConfigHash && r.SourceID == "" && r.TargetID == ""
	}
	for _, w := range records {
		if r.Name != w.WorkloadSlug {
			continue
		}
		switch r.Kind {
		case "workload":
			return r.SourceID == w.SourceDeploymentID && r.SourceVersion == w.SourceHash && (r.TargetID == "" || r.TargetID == w.TargetDeploymentID)
		case "variables", "secrets":
			return r.SourceID == w.AppID && r.TargetID == w.AppID && r.SourceVersion == w.SourceValuesHash
		case "workload_settings":
			return r.SourceID == "" && r.TargetID == "" && r.SourceVersion == w.SourceSettingsHash
		case "route_policy", "edge_policy":
			return r.SourceID == "" && r.TargetID == "" && r.SourceVersion == w.SourcePoliciesHash
		}
	}
	return false
}
