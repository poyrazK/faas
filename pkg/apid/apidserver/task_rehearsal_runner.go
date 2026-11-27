package apidserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/apid/taskrehearsal"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func (s *server) taskRehearsalRunner() *taskrehearsal.Runner {
	return &taskrehearsal.Runner{
		AdmissionEnabled: !s.releasePhaseOff,
		Store:            s.store, Submit: s.enqueueHostedTaskRehearsal, RemoveSource: removeHostedTaskBuildSource,
	}
}

func (s *server) enqueueHostedTaskRehearsal(ctx context.Context, input taskrehearsal.BuildInput) (apidsource.EnqueueResult, error) {
	var zero apidsource.EnqueueResult
	if !input.Account.MayDeploy() {
		return zero, input.Account.DeployBlockedProblem()
	}
	if !input.Account.EmailVerified() {
		return zero, api.ErrEmailVerificationRequired()
	}
	limits, ok := api.LimitsFor(input.Account.Plan)
	if !ok || int64(len(input.Archive)) > int64(limits.SourceTarballMaxMB)<<20 {
		return zero, state.ErrInvalidArgument
	}
	rate, err := s.consumeAccountDeployRate(ctx, input.Account, timeNow().UTC())
	if err != nil {
		return zero, err
	}
	if !rate.Allowed {
		return zero, state.ErrConflict
	}
	_, buildID := apidsource.OperationIDs(input.OperationID, input.App.ID)
	path, err := stageHostedTaskBuildSource(buildID, input.Archive)
	if err != nil {
		return zero, err
	}
	if problem := validateTarballShape(path); problem != nil {
		return zero, problem
	}
	if problem := scanForStatefulShapeAtRoot(path, false, ""); problem != nil {
		return zero, problem
	}
	manifest, problem := loadSourceRefManifest(path, input.App, input.Account.Plan, false)
	if problem != nil {
		return zero, problem
	}
	if manifest != nil && manifest.Build != nil {
		return zero, state.ErrConflict
	}
	params := apidsource.EnqueueParams{
		AppID: input.App.ID, OperationID: input.OperationID, Kind: state.DeploymentKindTarball,
		Scope: api.DefaultEnvScope, SourcePath: path, SourceBytes: int64(len(input.Archive)),
		ExpectedSourceSHA256: toolpublication.Digest(input.Archive),
		LogSpool:             spoolRoot(), Log: s.log, ActorUserID: input.Account.ID, TrafficPercentExplicit: true,
	}
	if input.Migration {
		params.ReleaseCommand = taskrehearsal.MigrationCommand()
	}
	return apidsource.Enqueue(ctx, s.store, s.notif, params)
}

func hostedTaskBuildSourcePath(buildID string) string {
	return filepath.Join(spoolRoot(), "hosted-task-"+buildID+".tar.gz")
}

func stageHostedTaskBuildSource(buildID string, archive []byte) (string, error) {
	if err := os.MkdirAll(spoolRoot(), 0o770); err != nil {
		return "", err
	}
	if err := os.Chmod(spoolRoot(), 0o770); err != nil {
		return "", err
	}
	path := hostedTaskBuildSourcePath(buildID)
	//nolint:forbidigo // The build identity and the deployment spool root are server-generated.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write(archive)
	return path, errors.Join(writeErr, file.Close())
}

func removeHostedTaskBuildSource(ctx context.Context, buildID string) error {
	id, err := uuid.Parse(buildID)
	if err != nil || id.String() != buildID || id.Version() != 5 {
		return state.ErrInvalidArgument
	}
	local := os.Remove(hostedTaskBuildSourcePath(buildID))
	if errors.Is(local, os.ErrNotExist) {
		local = nil
	}
	return errors.Join(local, apidsource.RemoveRehearsalSource(ctx, buildID))
}
