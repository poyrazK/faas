package promotion

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

func VerifyRecords(ctx context.Context, store state.Store, acct state.Account, promotion state.ProjectEnvironmentPromotion) error {
	_, workloads, err := store.ProjectEnvironmentPromotionByID(ctx, acct.ID, promotion.ProjectSlug, promotion.ToEnvironment, promotion.ID)
	if err != nil {
		return fmt.Errorf("could not load promotion verification checkpoints: %w", err)
	}
	if promotion.ReleaseGraphMode {
		if err := VerifyGraph(ctx, store, acct, promotion, workloads); err != nil {
			return err
		}
	}
	for _, workload := range workloads {
		app, err := store.AppBySlug(ctx, workload.WorkloadSlug)
		if err != nil || app.AccountID != acct.ID || app.ProjectID != promotion.ProjectID {
			return fmt.Errorf("workload %q is no longer in the project", workload.WorkloadSlug)
		}
		source, err := store.DeploymentByID(ctx, workload.SourceDeploymentID)
		if err != nil || source.AppID != app.ID || source.Scope != promotion.FromEnvironment {
			return fmt.Errorf("workload %q has no source deployment", workload.WorkloadSlug)
		}
		if workload.TargetDeploymentID == "" {
			return fmt.Errorf("workload %q has no target deployment", workload.WorkloadSlug)
		}
		target, err := store.DeploymentByID(ctx, workload.TargetDeploymentID)
		if err != nil {
			return fmt.Errorf("could not load target deployment for workload %q: %w", workload.WorkloadSlug, err)
		}
		if target.AppID != app.ID || target.Scope != promotion.ToEnvironment || target.Status != state.DeployLive {
			return fmt.Errorf("workload %q target deployment is not live in %s", workload.WorkloadSlug, promotion.ToEnvironment)
		}
		if workload.Status == "promoted" && target.Reason != "environment promotion/"+promotion.ID {
			return fmt.Errorf("workload %q target deployment is not owned by this promotion", workload.WorkloadSlug)
		}
		if workload.Status == "promoted" && !SameArtifact(source, target) {
			return fmt.Errorf("workload %q target artifact does not match the source release", workload.WorkloadSlug)
		}
		if _, err := store.UpdateProjectEnvironmentPromotionVerificationWorkload(ctx, acct.ID, promotion.ID,
			workload.ID, "verified", ""); err != nil {
			return fmt.Errorf("could not record verification for workload %q: %w", workload.WorkloadSlug, err)
		}
	}
	if promotion.ReleaseGraphMode {
		// Re-read after per-workload verification so a graph replacement during
		// the checks cannot be reported as a successful promotion.
		if err := VerifyGraph(ctx, store, acct, promotion, workloads); err != nil {
			return err
		}
	}

	return nil
}

func VerifyGraph(ctx context.Context, store state.Store, acct state.Account, promotion state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload) error {
	if promotion.TargetReleaseSetID == "" {
		return errors.New("promoted release graph ID was not recorded")
	}
	reader, ok := store.(state.ProjectReleaseSetReader)
	if !ok {
		return errors.New("release inventory unavailable")
	}
	active, err := reader.ActiveProjectReleaseSet(ctx, acct.ID, promotion.ProjectID, promotion.ToEnvironment)
	if err != nil {
		return errors.New("could not load active promoted release graph")
	}
	if active.ID != promotion.TargetReleaseSetID {
		return errors.New("active target release graph changed during promotion verification")
	}
	members := make([]state.ProjectReleaseMember, 0, len(workloads))
	for _, workload := range workloads {
		app, err := store.AppBySlug(ctx, workload.WorkloadSlug)
		if err != nil || app.AccountID != acct.ID || app.ProjectID != promotion.ProjectID || workload.TargetDeploymentID == "" {
			return fmt.Errorf("workload %q has no target graph member", workload.WorkloadSlug)
		}
		members = append(members, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: workload.TargetDeploymentID})
	}
	if !ReleaseSetMatchesMembers(active, members) {
		return errors.New("active target release graph does not match the promotion checkpoints")
	}
	if promotion.SyncConfig {
		publisher, ok := store.(state.ProjectEnvironmentPromotionReleaseSetStore)
		if !ok {
			return errors.New("atomic promotion configuration verification is unavailable")
		}
		if _, err := publisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, acct.ID, promotion.ID, promotion.ReleaseTTLSeconds, members); err != nil {
			return errors.New("target configuration or feature flags changed during promotion verification")
		}
	}
	return nil
}

func SameArtifact(source, target state.Deployment) bool {
	if source.Kind != target.Kind || source.SourceSHA256 != target.SourceSHA256 ||
		source.ImageDigest != target.ImageDigest || source.CommitSHA != target.CommitSHA {
		return false
	}
	if source.RootfsKey != "" || target.RootfsKey != "" {
		return source.RootfsKey != "" && source.RootfsKey == target.RootfsKey && source.RootfsBytes == target.RootfsBytes
	}
	return source.RootfsPath != "" && source.RootfsPath == target.RootfsPath && source.RootfsBytes == target.RootfsBytes
}

func ReleaseSetMatchesMembers(release state.ProjectReleaseSet, members []state.ProjectReleaseMember) bool {
	if len(release.Members) != len(members) {
		return false
	}
	byApp := make(map[string]string, len(release.Members))
	for _, member := range release.Members {
		byApp[member.AppID] = member.DeploymentID
	}
	for _, member := range members {
		if byApp[member.AppID] != member.DeploymentID {
			return false
		}
	}
	return true
}

func SelectedDeployment(ctx context.Context, store state.Store, app state.App, environment string, release *state.ProjectReleaseSet) (state.Deployment, error) {
	if release == nil {
		return store.LiveDeploymentForScope(ctx, app.ID, environment)
	}
	for _, member := range release.Members {
		if member.AppID != app.ID {
			continue
		}
		deployment, err := store.DeploymentByID(ctx, member.DeploymentID)
		if err != nil {
			return state.Deployment{}, err
		}
		if deployment.AppID != app.ID || deployment.Scope != environment || deployment.Status != state.DeployLive {
			return state.Deployment{}, state.ErrConflict
		}
		return deployment, nil
	}
	return state.Deployment{}, state.ErrConflict
}
