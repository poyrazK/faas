package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectEnvironmentCloneEnvironmentBorrowers describes direct environment
// references and scoped deployment rows. Counts retain expired/inactive rows,
// include inconsistent tenant metadata, and never convey deletion authority.
// Copied references remain in the counts until their authenticated cleanup.
type ProjectEnvironmentCloneEnvironmentBorrowers struct {
	OperationID, EnvironmentID, OwnershipSHA256       string
	NativePlanSHA256, NativeObservationSHA256         string
	ConfigurationCleanupSHA256, WorkloadCleanupSHA256 string
	References                                        map[string]int64
	OwnedDeployments, ForeignDeployments              int64
}

// ProjectEnvironmentCloneEnvironmentBorrowersForLease authenticates original
// ownership and completed native closure before inventorying environment
// metadata. This private report can be used before or after catalogue cleanup.
// A zero count does not prove operational closure or authorize a cascade.
func (s *PgStore) ProjectEnvironmentCloneEnvironmentBorrowersForLease(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneEnvironmentBorrowers, error) {
	var out ProjectEnvironmentCloneEnvironmentBorrowers
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		native, err := readCheckpointWorkloadRetirementTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if native.State != "retired" || native.Plan.EnvironmentID == "" {
			return ErrConflict
		}
		ownership, err := verifyCloneConfigurationOwnershipTx(ctx, tx, op, native.Plan.EnvironmentID)
		if err != nil {
			return err
		}
		if ownership == "" {
			return ErrConflict
		}
		q := sqlc.New()
		config, configErr := q.ReadCloneConfigurationCleanup(ctx, tx, mustPgUUID(op.ID))
		if configErr != nil && !errors.Is(mapErr(configErr), ErrNotFound) {
			return mapErr(configErr)
		}
		workloads, workloadErr := q.ReadCloneWorkloadCleanup(ctx, tx, mustPgUUID(op.ID))
		if workloadErr != nil && !errors.Is(mapErr(workloadErr), ErrNotFound) {
			return mapErr(workloadErr)
		}
		if workloadErr == nil && configErr != nil {
			return ErrConflict
		}
		row, err := q.ReadCloneEnvironmentMetadataBorrowers(ctx, tx, sqlc.ReadCloneEnvironmentMetadataBorrowersParams{OperationID: mustPgUUID(op.ID), ProjectID: mustPgUUID(op.ProjectID), EnvironmentID: mustPgUUID(native.Plan.EnvironmentID), Environment: op.TargetEnvironment})
		if err != nil {
			return mapErr(err)
		}
		out = ProjectEnvironmentCloneEnvironmentBorrowers{OperationID: op.ID, EnvironmentID: native.Plan.EnvironmentID, OwnershipSHA256: ownership, NativePlanSHA256: cloneCheckpointSHA(native.Plan), NativeObservationSHA256: native.ObservationSHA256, OwnedDeployments: row.OwnedDeployments, ForeignDeployments: row.ForeignDeployments, References: cloneEnvironmentBorrowerCounts(row)}
		// Native replay already verified each present receipt and its exact post-state.
		if configErr == nil {
			out.ConfigurationCleanupSHA256 = cloneConfigurationCleanupSHA(config)
		}
		if workloadErr == nil {
			out.WorkloadCleanupSHA256 = cloneWorkloadCleanupSHA(workloads)
		}
		return nil
	})
	if err != nil {
		return ProjectEnvironmentCloneEnvironmentBorrowers{}, err
	}
	return out, nil
}

func cloneWorkloadCleanupSHA(row sqlc.ProjectEnvironmentCloneWorkloadCleanup) string {
	return cloneCheckpointSHA(struct {
		OperationID, ConfigurationCleanupSHA256, ManifestSHA256 string
		CleanedAt                                               time.Time
	}{pgUUIDString(row.OperationID), row.ConfigurationCleanupSha256, row.ManifestSha256, row.CleanedAt.Time.UTC()})
}

func cloneEnvironmentBorrowerCounts(row sqlc.ReadCloneEnvironmentMetadataBorrowersRow) map[string]int64 {
	return map[string]int64{
		"app_environment_route_budgets":           row.AppEnvironmentRouteBudgets,
		"app_environment_secret_ref_suppressions": row.AppEnvironmentSecretRefSuppressions,
		"app_environment_secret_refs":             row.AppEnvironmentSecretRefs,
		"app_environment_workload_intents":        row.AppEnvironmentWorkloadIntents,
		"custom_domains":                          row.CustomDomains,
		"dev_bridge_sessions":                     row.DevBridgeSessions,
		"environment_external_field_owners":       row.EnvironmentExternalFieldOwners,
		"environment_git_sources":                 row.EnvironmentGitSources,
		"environment_managed_fields":              row.EnvironmentManagedFields,
		"environment_workload_graphs":             row.EnvironmentWorkloadGraphs,
		"environment_workload_retirements":        row.EnvironmentWorkloadRetirements,
		"feature_flag_versions":                   row.FeatureFlagVersions,
		"hosting_sites":                           row.HostingSites,
		"invocation_environment_queue_admissions": row.InvocationEnvironmentQueueAdmissions,
		"invocation_work_environment_admissions":  row.InvocationWorkEnvironmentAdmissions,
		"invocation_work_environment_domains":     row.InvocationWorkEnvironmentDomains,
		"invocations":                             row.Invocations,
		"project_environment_edge_policies":       row.ProjectEnvironmentEdgePolicies,
		"project_environment_qualifications":      row.ProjectEnvironmentQualifications,
		"project_environment_queue_runtime_sets":  row.ProjectEnvironmentQueueRuntimeSets,
		"project_environment_route_policies":      row.ProjectEnvironmentRoutePolicies,
		"project_environment_workload_specs":      row.ProjectEnvironmentWorkloadSpecs,
		"project_release_sets":                    row.ProjectReleaseSets,
		"project_release_stages":                  row.ProjectReleaseStages,
		"runtime_environment_scaling_states":      row.RuntimeEnvironmentScalingStates,
	}
}
