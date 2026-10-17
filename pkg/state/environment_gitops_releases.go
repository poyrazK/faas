package state

// adr: 1546. Recovery inventory reuses rollback checks without extending retention.
import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

type EnvironmentGitOpsRecoveryStore interface {
	ListEnvironmentGitReleases(context.Context, EnvironmentGitSource, api.ListEnvironmentGitReleasesRequest, environmentsync.DesiredState) (api.ListEnvironmentGitReleasesResponse, error)
}

func recoveryListing(source EnvironmentGitSource, request api.ListEnvironmentGitReleasesRequest, desired environmentsync.DesiredState, now time.Time) (api.ListEnvironmentGitReleasesResponse, error) {
	out := api.ListEnvironmentGitReleasesResponse{}
	if request.Validate() != nil {
		return out, ErrInvalidArgument
	}
	if err := validateEnvironmentGitOpsPreview(source, request.TargetCommitSHA, desired); err != nil {
		return out, err
	}
	return api.ListEnvironmentGitReleasesResponse{Project: desired.Definition.Project, Environment: source.EnvironmentSlug, SourceID: source.ID, TargetCommitSHA: request.TargetCommitSHA, ObservedAt: now, ReadOnly: true, ValuesRedacted: true, RequiresTargetQualification: true, Releases: []api.EnvironmentGitRecoveryRelease{}}, nil
}

func recoveryRelease(release ProjectReleaseSet, plan api.PreviewEnvironmentGitRollbackResponse, err error, now time.Time) (api.EnvironmentGitRecoveryRelease, error) {
	out := api.EnvironmentGitRecoveryRelease{ReleaseSetID: release.ID, Active: release.Active, CreatedAt: release.CreatedAt, ExpiresAt: cloneProjectReleaseSet(release).ExpiresAt, RetentionStatus: "unknown", Eligibility: "blocked", BlockingReasons: []string{}, ProtectionStatus: "disabled", Artifacts: []api.EnvironmentGitPromotionArtifact{}}
	switch {
	case release.Active:
		out.RetentionStatus = "active"
	case release.ExpiresAt == nil:
		out.RetentionStatus = "unknown"
	case release.ExpiresAt.After(now):
		out.RetentionStatus = "compatibility_window"
	default:
		out.RetentionStatus = "compatibility_window_elapsed"
	}
	if errors.Is(err, ErrEnvironmentGitOpsRollbackReleaseUnproven) {
		out.BlockingReasons = []string{"rollback_release_unproven"}
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.CommitSHA, out.ServedAt, out.PlanHash = plan.Origin.CommitSHA, plan.OriginServedAt, plan.PlanHash
	out.BlockingReasons = append(out.BlockingReasons, plan.BlockingReasons...)
	out.Artifacts = append(out.Artifacts, plan.Artifacts...)
	if len(out.BlockingReasons) == 0 {
		out.Eligibility = "eligible"
	}
	return out, nil
}
