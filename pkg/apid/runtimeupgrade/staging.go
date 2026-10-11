package runtimeupgrade

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// StagingStore is private apid admission for an already reserved candidate.
// Candidate reservation must retain reviewed overrides and explicit zero weight.
type StagingStore interface {
	state.RuntimeUpgradeOperationStore
	AppByID(context.Context, string) (state.App, error)
	AccountByID(context.Context, string) (state.Account, error)
	DeploymentByID(context.Context, string) (state.Deployment, error)
}

var ErrSourceIntegrity = errors.New("runtime upgrade: retained source integrity violation")

type Stager struct {
	Store     StagingStore
	Source    storage.StorageBackend // same source handoff backend used by builderd
	SpoolRoot string                 // trusted apid/builderd source spool; absolute
}

// CandidatePath lets private reservation bind the destination before staging.
// The operation UUID is also its stable build/source-object UUID.
func (s Stager) CandidatePath(operationID string) (string, error) {
	id, err := uuid.Parse(operationID)
	if err != nil || id == uuid.Nil || id.String() != operationID || !filepath.IsAbs(s.SpoolRoot) {
		return "", state.ErrInvalidArgument
	}
	return filepath.Join(s.SpoolRoot, operationID+".tar.gz"), nil
}

// StageAndRegister verifies bytes before publishing either handoff, then calls
// the atomic registration fence. It does not fetch Git refs, queue builds,
// change traffic or create customer intent. Retain the request across retries.
func (s Stager) StageAndRegister(ctx context.Context, r state.RuntimeUpgradeOperationRequest) (state.RuntimeUpgradeOperation, error) {
	if err := ctx.Err(); err != nil {
		return state.RuntimeUpgradeOperation{}, err
	}
	if s.Store == nil || !validStagingRequest(r) {
		return state.RuntimeUpgradeOperation{}, state.ErrInvalidArgument
	}
	// A lost registration response must not republish an in-flight source or
	// turn historical completion into another activation after rollback.
	old, err := s.Store.RuntimeUpgradeOperation(ctx, r.AccountID, r.ID)
	if err == nil {
		if old.RuntimeUpgradeOperationRequest != r {
			return state.RuntimeUpgradeOperation{}, state.ErrConflict
		}
		return old, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.RuntimeUpgradeOperation{}, fmt.Errorf("read staged runtime upgrade: %w", err)
	}
	serving, err := s.stagingInputs(ctx, r)
	if err != nil {
		return state.RuntimeUpgradeOperation{}, fmt.Errorf("runtime upgrade staging inputs: %w", err)
	}
	if err := s.stageSource(ctx, r.ID, serving); err != nil {
		return state.RuntimeUpgradeOperation{}, fmt.Errorf("stage runtime upgrade source: %w", err)
	}
	// Rechecks ownership, serving identity, qualification and input revisions
	// under the existing database fences after potentially slow storage I/O.
	op, err := s.Store.RegisterRuntimeUpgradeOperation(ctx, r)
	if err != nil {
		// Do not delete either handoff: a commit response may have been lost.
		return state.RuntimeUpgradeOperation{}, fmt.Errorf("register staged runtime upgrade: %w", err)
	}
	return op, nil
}

func validStagingRequest(r state.RuntimeUpgradeOperationRequest) bool {
	for i, text := range []string{r.ID, r.AccountID, r.AppID, r.DeploymentID, r.ServingDeploymentID} {
		id, err := uuid.Parse(text)
		if err != nil || id == uuid.Nil || (i == 0 && id.String() != text) {
			return false
		}
	}
	for _, text := range []string{r.TargetReleaseID, r.SourceSHA256, r.QualificationReportSHA256} {
		if len(text) != 64 || strings.Trim(text, "0123456789abcdef") != "" {
			return false
		}
	}
	return r.DeploymentID != r.ServingDeploymentID
}

func (s Stager) stagingInputs(ctx context.Context, r state.RuntimeUpgradeOperationRequest) (state.Deployment, error) {
	app, err := s.Store.AppByID(ctx, r.AppID)
	if err != nil {
		return state.Deployment{}, err
	}
	if app.AccountID != r.AccountID {
		return state.Deployment{}, state.ErrNotFound
	}
	if app.Status != state.AppActive || app.Type != state.AppTypeFunction || app.Manifest.BuildDockerfile != "" ||
		app.Manifest.ExecutionMode == "job" || app.Manifest.ExecutionMode == "service" {
		return state.Deployment{}, state.ErrConflict
	}
	account, err := s.Store.AccountByID(ctx, r.AccountID)
	if err != nil {
		return state.Deployment{}, err
	}
	limit, known := api.LimitsFor(account.Plan)
	if !known || !account.MayDeploy() {
		return state.Deployment{}, state.ErrConflict
	}
	candidate, err := s.Store.DeploymentByID(ctx, r.DeploymentID)
	if err != nil {
		return state.Deployment{}, err
	}
	serving, err := s.Store.DeploymentByID(ctx, r.ServingDeploymentID)
	if err != nil {
		return state.Deployment{}, err
	}
	path, err := s.CandidatePath(r.ID)
	if err != nil {
		return state.Deployment{}, err
	}
	if candidate.AppID != r.AppID || serving.AppID != r.AppID || candidate.DeletedAt != nil || serving.DeletedAt != nil ||
		candidate.Status != state.DeployPending || candidate.BuildID != "" || candidate.ImageDigest != "" || candidate.RootfsKey != "" || candidate.RootfsPath != "" ||
		candidate.SourcePath != path || candidate.TrafficPercent != 0 || !candidate.TrafficPercentExplicit || candidate.CanaryTotalSteps != 0 ||
		state.IsServiceRollout(candidate) || candidate.EnvironmentWorkloadHeld() ||
		serving.Status != state.DeployLive || serving.TrafficPercent != 100 ||
		(candidate.Kind != state.DeploymentKindTarball && candidate.Kind != state.DeploymentKindGitHub && candidate.Kind != state.DeploymentKindPreview) || serving.Kind != candidate.Kind ||
		candidate.SourceSHA256 != r.SourceSHA256 || serving.SourceSHA256 != r.SourceSHA256 ||
		candidate.SourceBytes != serving.SourceBytes || serving.SourceBytes <= 0 || serving.SourceBytes > int64(limit.SourceTarballMaxMB)*1024*1024 ||
		candidate.SourceRoot != serving.SourceRoot || candidate.Handler != serving.Handler ||
		candidate.GitHubSourceRef != "" || candidate.GitHubInstallationID != 0 {
		return state.Deployment{}, state.ErrConflict
	}
	return serving, nil
}
