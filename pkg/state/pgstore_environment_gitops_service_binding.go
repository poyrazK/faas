package state

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ EnvironmentGitOpsServiceBindingStore = (*PgStore)(nil)

func (s *PgStore) ResolveEnvironmentGitOpsServiceBinding(ctx context.Context, callerAppID, callerDeploymentID, service string) (EnvironmentGitOpsServiceBindingRoute, bool, bool, error) {
	var zero EnvironmentGitOpsServiceBindingRoute
	service = strings.ToLower(strings.TrimSpace(service))
	callerID, callerErr := operationUUID(callerAppID)
	if callerErr != nil || !api.ValidAppSlug(service) {
		return zero, false, false, ErrInvalidArgument
	}
	if callerDeploymentID == "" {
		var managed bool
		err := s.pool.QueryRow(ctx, `
			select exists (
			  select 1 from project_release_sets rs
			  join project_release_members member on member.release_id=rs.id
			  join deployments deployment on deployment.id=member.deployment_id
			 where rs.active and member.app_id=$1::uuid
			   and deployment.environment_workload_runtime is not null
			)`, callerAppID).Scan(&managed)
		if err != nil {
			return zero, false, false, err
		}
		if managed {
			return zero, true, false, ErrConflict
		}
		return zero, false, false, nil
	}
	callerDepID, depErr := operationUUID(callerDeploymentID)
	if depErr != nil {
		return zero, false, false, ErrInvalidArgument
	}
	callerDeployment, err := s.DeploymentByID(ctx, callerDeploymentID)
	if err != nil {
		return zero, false, false, err
	}
	if callerDeployment.AppID != callerAppID {
		return zero, false, false, ErrConflict
	}
	if !callerDeployment.EnvironmentWorkloadManaged() {
		return zero, false, false, nil
	}
	caller, err := callerDeployment.ScopedWorkloadRuntime()
	if err != nil || caller == nil {
		return zero, true, false, ErrConflict
	}
	callerApp, err := s.AppByID(ctx, callerAppID)
	if err != nil {
		return zero, true, false, err
	}
	if callerApp.Status == AppDeleted || callerApp.AccountID == "" {
		return zero, true, false, ErrConflict
	}
	var enforce bool
	if err := s.pool.QueryRow(ctx, `select exists(
		select 1 from active_environment_git_sources source
		where source.id=$1::uuid and source.account_id=$2::uuid and source.project_id=$3::uuid
		  and source.environment_id=$4::uuid and source.mode='enforce' and not source.suspended
		)`, caller.SourceID, callerApp.AccountID, callerApp.ProjectID, caller.EnvironmentID).Scan(&enforce); err != nil {
		return zero, true, false, err
	}
	if !enforce {
		return zero, true, false, ErrConflict
	}
	var release ProjectReleaseSet
	err = s.pool.QueryRow(ctx, `
		select release.id::text, release.account_id::text, release.project_id::text, release.environment_slug
		  from project_release_sets release
		  join project_release_members member on member.release_id=release.id
		 where release.active and release.project_id=$1::uuid and release.environment_slug=$2
		   and member.app_id=$3::uuid and member.deployment_id=$4::uuid`,
		callerApp.ProjectID, normalizedDeploymentScope(callerDeployment.Scope), callerID, callerDepID).Scan(
		&release.ID, &release.AccountID, &release.ProjectID, &release.EnvironmentSlug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, true, false, ErrConflict
		}
		return zero, true, false, err
	}
	release.Active = true
	if release.AccountID != callerApp.AccountID || release.ProjectID != callerApp.ProjectID ||
		normalizedDeploymentScope(release.EnvironmentSlug) != normalizedDeploymentScope(callerDeployment.Scope) ||
		callerDeployment.Status != DeployLive || callerDeployment.EnvironmentWorkloadHeld() {
		return zero, true, false, ErrConflict
	}

	var targetAppID string
	for _, binding := range caller.ServiceBindings {
		if strings.EqualFold(binding.Workload, service) {
			if targetAppID != "" && targetAppID != binding.TargetAppID {
				return zero, true, false, ErrConflict
			}
			targetAppID = binding.TargetAppID
		}
	}
	if targetAppID == "" {
		return zero, true, false, nil
	}
	targetUUID, err := operationUUID(targetAppID)
	if err != nil {
		return zero, true, false, ErrConflict
	}
	var targetDeploymentID string
	if err := s.pool.QueryRow(ctx, `select deployment_id::text from project_release_members where release_id=$1::uuid and app_id=$2::uuid`, release.ID, targetUUID).Scan(&targetDeploymentID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, true, false, ErrConflict
		}
		return zero, true, false, err
	}
	targetDeployment, err := s.DeploymentByID(ctx, targetDeploymentID)
	if err != nil {
		return zero, true, false, err
	}
	if targetDeployment.AppID != targetAppID || targetDeployment.Status != DeployLive || targetDeployment.EnvironmentWorkloadHeld() ||
		normalizedDeploymentScope(targetDeployment.Scope) != normalizedDeploymentScope(callerDeployment.Scope) {
		return zero, true, false, ErrConflict
	}
	targetApp, err := s.AppByID(ctx, targetAppID)
	if err != nil {
		return zero, true, false, err
	}
	if targetApp.Status == AppDeleted || targetApp.AccountID != callerApp.AccountID || targetApp.ProjectID != callerApp.ProjectID {
		return zero, true, false, ErrConflict
	}
	target, err := targetDeployment.ScopedWorkloadRuntime()
	if err != nil || target == nil || target.Resource != "workload/"+service ||
		caller.SourceID != target.SourceID || caller.EnvironmentID != target.EnvironmentID || caller.RevisionID != target.RevisionID ||
		caller.Generation != target.Generation || caller.IntentVersion != target.IntentVersion || caller.PlanHash != target.PlanHash ||
		caller.DefinitionDigest != target.DefinitionDigest || target.WorkloadClass == WorkloadClassJob || target.WorkloadClass == WorkloadClassWorker {
		return zero, true, false, ErrConflict
	}
	requireHTTPS, callScope, reliability, err := environmentGitOpsServiceBindingPolicy(*caller, *target, service)
	if err != nil {
		return zero, true, false, err
	}
	return EnvironmentGitOpsServiceBindingRoute{
		CallerAppID: callerAppID, CallerDeploymentID: callerDeploymentID,
		TargetAppID: targetAppID, TargetDeploymentID: targetDeploymentID,
		ReleaseSetID: release.ID, AccountID: callerApp.AccountID,
		RequireHTTPS: requireHTTPS, CallScope: callScope, Reliability: reliability,
	}, true, true, nil
}
