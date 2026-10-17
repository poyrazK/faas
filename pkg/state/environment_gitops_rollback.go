package state

// adr: 20261017190805. Historical serving evidence selects code, never execution authority.
import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/rollback"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var ErrEnvironmentGitOpsRollbackReleaseUnproven = errors.New("state: retained GitOps serving release is unproven")

type EnvironmentGitOpsRollbackStore interface {
	PreviewEnvironmentGitOpsRollback(context.Context, EnvironmentGitSource, api.PreviewEnvironmentGitRollbackRequest, environmentsync.DesiredState) (api.PreviewEnvironmentGitRollbackResponse, error)
}

func validateGitRollback(source EnvironmentGitSource, request api.PreviewEnvironmentGitRollbackRequest, desired environmentsync.DesiredState) error {
	if request.Validate() != nil {
		return ErrInvalidArgument
	}
	return validateEnvironmentGitOpsPreview(source, request.TargetCommitSHA, desired)
}

func retainedRollbackInput(source EnvironmentGitSource, request api.PreviewEnvironmentGitRollbackRequest, desired environmentsync.DesiredState, release ProjectReleaseSet, graph EnvironmentWorkloadGraph, revision EnvironmentDesiredRevision, receipt EnvironmentWorkloadServingReceipt) (rollback.Input, error) {
	if release.ID != request.ReleaseSetID || release.AccountID != source.AccountID || release.ProjectID != source.ProjectID || release.EnvironmentSlug != source.EnvironmentSlug {
		return rollback.Input{}, ErrNotFound
	}
	if graph.ID == "" || graph.SourceID != source.ID || graph.EnvironmentID != source.EnvironmentID || revision.SourceID != source.ID || graph.RevisionID != revision.ID {
		return rollback.Input{}, ErrEnvironmentGitOpsRollbackReleaseUnproven
	}
	targets := map[string]string{}
	for _, member := range graph.Members {
		targets[member.Resource] = releaseMemberForApp(release, member.AppID)
	}
	proven := graph.Phase == "prepared" && graph.PreparedAt != nil && graph.ErrorCode == "" && graph.DefinitionDigest == revision.Digest &&
		receipt.SourceID == graph.SourceID && receipt.SourceGeneration == graph.Generation && receipt.IntentVersion == graph.IntentVersion && receipt.RevisionID == graph.RevisionID && receipt.PlanHash == graph.PlanHash &&
		environmentWorkloadServingReceiptMatches(receipt, graph, release.ID, receipt.ExpectedGateways, targets)
	provider := source.Spec.Provider
	if provider == "" {
		provider = "github"
	}
	return rollback.Input{Source: source, Revision: revision, Origin: api.EnvironmentGitPromotionOrigin{SourceID: source.ID, RevisionID: revision.ID, CommitSHA: revision.CommitSHA, DefinitionDigest: revision.Digest, Generation: graph.Generation, IntentVersion: graph.IntentVersion, GraphID: graph.ID, ReleaseSetID: release.ID, Provider: provider, RepositoryID: source.Spec.RepositoryID},
		ServedAt: receipt.ServedAt, Proven: proven, TargetCommit: request.TargetCommitSHA, Target: desired, Now: time.Now().UTC()}, nil
}

func rollbackMemberCompatible(input *rollback.Input, member EnvironmentWorkloadGraphMember, deployment Deployment, observed EnvironmentGitOpsObservation) bool {
	if observed.State.ResourceIDs[member.Resource] != member.AppID {
		input.BlockingReasons = append(input.BlockingReasons, member.Resource+": rollback_target_identity_changed")
		return false
	}
	if member.ExecutionMode == api.ExecutionModeJob && !member.ScheduleConfigured || len(deployment.Sidecars) != 0 && string(deployment.Sidecars) != "[]" && string(deployment.Sidecars) != "null" {
		input.BlockingReasons = append(input.BlockingReasons, member.Resource+": rollback_workload_unsupported")
		return false
	}
	return deployment.ID != "" && deployment.AppID == member.AppID && normalizedDeploymentScope(deployment.Scope) == input.Source.EnvironmentSlug && deployment.DeletedAt == nil
}

func finishGitRollback(input rollback.Input, observed EnvironmentGitOpsObservation, expected string) (api.PreviewEnvironmentGitRollbackResponse, error) {
	out, proposed, err := rollback.Prepare(input)
	if err != nil {
		return out, ErrInvalidArgument
	}
	preview, err := environmentGitOpsCommitPreview(input.Source, input.TargetCommit, proposed, observed)
	if err != nil {
		return out, err
	}
	out, err = rollback.Finish(out, preview.Plan)
	if err == nil && expected != "" && expected != out.PlanHash {
		return api.PreviewEnvironmentGitRollbackResponse{}, ErrConflict
	}
	return out, err
}
