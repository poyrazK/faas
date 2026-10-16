package apidserver

import (
	"context"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) prepareSafeProjectCommands(ctx context.Context, acct state.Account, project state.Project, req *scanPlanRequest, apps []state.App, scan reposcan.Result) *api.Problem {
	source, _, err := s.stageApplyTarball(ctx, req.ScanDir, acct.ID, project.ID, apps[0], false)
	if err != nil {
		return api.ErrValidation("migration source could not be inspected")
	}
	defer func() { _ = os.Remove(source) }()
	var command *api.ProjectReleaseCommand
	for i, app := range apps {
		workload := scan.Workloads[i]
		resolved := deploymentReleaseCommand{command: workload.ReleaseCommand, shell: workload.ReleaseCommandShell}
		manifest, problem := loadSourceRefManifest(source, app, acct.Plan)
		if problem != nil {
			return problem
		}
		if len(sourceOperationSpecs(manifest)) > 0 {
			return api.ErrValidation("atomic migration deploy cannot change operation declarations")
		}
		if len(apps) == 1 {
			resolved, problem = resolveSourceReleaseCommand(source, app, manifest)
			if problem != nil {
				return problem
			}
		} else if manifest != nil && manifest.Release != nil && manifest.Release.Command != "" {
			return api.ErrValidation("multi-workload migration deploy requires a single Procfile release declaration")
		}
		if len(resolved.command) == 0 {
			continue
		}
		if command != nil {
			return api.ErrValidation("atomic migration deploy supports one release command per project")
		}
		command = &api.ProjectReleaseCommand{AppID: app.ID, Slug: app.Slug, CommandHash: api.ProjectReleaseCommandHash(resolved.command, resolved.shell), Compatibility: "backward-compatible", Status: "waiting", VerifyBaseline: true}
		if manifest != nil && manifest.Release != nil && manifest.Release.ContractCommand != "" {
			if !s.projectMigrationContractsEnabled {
				return api.ErrCapacity("schema contract admission is disabled pending native qualification")
			}
			if req.SafeRelease.Verification == nil || !req.SafeRelease.Verification.Progressive || req.SafeRelease.Verification.Stabilization == nil || !req.SafeRelease.Verification.Stabilization.Enabled {
				return api.ErrValidation("release.contract_command requires --migrate --progressive --stabilize and paired configuration")
			}
			req.MigrationContractCommand = manifest.Release.ContractCommand
			command.ContractCommandHash = api.ProjectReleaseCommandHash([]string{req.MigrationContractCommand}, true)
		}

	}
	if command == nil {
		return api.ErrValidation("--migrate requires a declared release.command or Procfile release process")
	}
	req.SafeRelease.ReleaseCommand = command
	if tracker := deploystaging.ProjectSubmissionFromContext(ctx); tracker != nil {
		tracker.Receipt.Apply.SafeRelease = req.SafeRelease
	}
	return nil
}

func safeProjectCommandMatches(safe *api.ProjectSafeRelease, appID string, command deploymentReleaseCommand) bool {
	if safe.ReleaseCommand == nil {
		return len(command.command) == 0
	}
	if appID != safe.ReleaseCommand.AppID {
		return len(command.command) == 0
	}
	return len(command.command) > 0 && safe.ReleaseCommand.CommandHash == api.ProjectReleaseCommandHash(command.command, command.shell)
}

func (s *server) stageSafeProjectCommand(ctx context.Context, accountID, projectID string, req *scanPlanRequest) *api.Problem {
	if req.SafeRelease.ReleaseCommand == nil {
		return nil
	}
	tracker := deploystaging.ProjectSubmissionFromContext(ctx)
	store, ok := s.store.(state.ProjectReleaseCommandStore)
	if tracker == nil || !ok {
		return api.ErrCapacity("project migration orchestration is unavailable")
	}
	if err := store.StageProjectReleaseCommand(ctx, accountID, tracker.Receipt.AttemptID, projectID, req.SafeRelease); err != nil {
		return api.NewProblem(409, api.CodeConflict, "Migration admission conflict", "another migration is pending or the baseline changed; inspect the current project release before submitting a new attempt")
	}
	if req.MigrationContractCommand != "" {
		contractStore, ok := s.store.(state.ProjectMigrationContractStore)
		if !ok {
			return api.ErrCapacity("migration contract admission is unavailable")
		}
		if err := contractStore.StageProjectMigrationContract(ctx, accountID, tracker.Receipt.AttemptID, projectID, req.MigrationContractCommand); err != nil {
			return api.NewProblem(409, api.CodeConflict, "Contract plan conflicted", "contract source does not match the saved migration plan")
		}
	}
	return nil
}
