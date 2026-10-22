package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type CheckedProjectReleaseStore interface {
	DeactivateProjectReleaseWithBindings(context.Context, string, string, string, string, []ProjectReleaseMember, []BindingPromotionFence) (ProjectReleaseSet, error)
	RollbackProjectReleaseWithBindings(context.Context, string, string, string, string, string, int, []ProjectReleaseMember, []BindingPromotionFence) (ProjectReleaseSet, error)
	ActivateProjectReleaseWithBindings(context.Context, string, string, string, string, []ProjectReleaseMember, int, []ProjectReleaseMember, []BindingPromotionFence) (ProjectReleaseSet, error)
}

type checkedReleaseGraphKey struct{}

// The key is private and installed only after matching each checked recipient
// to the complete proposed graph. Public release selectors never carry it.
func checkedReleaseGraphContext(ctx context.Context, accountID, environment string, members []ProjectReleaseMember, fences []BindingPromotionFence) (context.Context, error) {
	if len(fences) != len(members) || len(members) == 0 {
		return ctx, ErrBindingReleaseRequired
	}
	byApp := make(map[string]string, len(members))
	for _, member := range members {
		if byApp[member.AppID] != "" {
			return ctx, ErrInvalidArgument
		}
		byApp[member.AppID] = member.DeploymentID
	}
	seen := map[string]bool{}
	for _, fence := range fences {
		if fence.AccountID != accountID || fence.Scope != normalizedDeploymentScope(environment) || byApp[fence.AppID] != fence.DeploymentID || seen[fence.AppID] || fence.AllowUnsupported {
			return ctx, ErrBindingReleaseRequired
		}
		seen[fence.AppID] = true
	}
	fences = append([]BindingPromotionFence(nil), fences...)
	sort.Slice(fences, func(i, j int) bool { return fences[i].AppID < fences[j].AppID })
	return context.WithValue(WithBindingReleaseFences(ctx, fences), checkedReleaseGraphKey{}, uuid.NewString()), nil
}

type checkedReleaseRollbackKey struct{}
type checkedReleaseRollback struct{ source, previous string }

func checkedReleaseRollbackContext(ctx context.Context, source, previous string) (context.Context, error) {
	for _, id := range []string{source, previous} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return ctx, ErrInvalidArgument
		}
	}
	if source == previous {
		return ctx, ErrConflict
	}
	return context.WithValue(ctx, checkedReleaseRollbackKey{}, checkedReleaseRollback{source, previous}), nil
}

// Retention of a deployment alone does not authorize recovery of expired history.
func validateCheckedRollbackSource(source ProjectReleaseSet, accountID, projectID, environment string, members map[string]string, now time.Time) error {
	if source.AccountID != accountID || source.ProjectID != projectID || source.EnvironmentSlug != environment || source.Active || source.ExpiresAt == nil || !now.Before(*source.ExpiresAt) || len(source.Members) != len(members) {
		return ErrConflict
	}
	seen := map[string]bool{}
	for _, member := range source.Members {
		if seen[member.AppID] || members[member.AppID] != member.DeploymentID {
			return ErrConflict
		}
		seen[member.AppID] = true
	}
	return nil
}

type checkedReleaseDeactivationKey struct{}

func checkedReleaseDeactivationContext(ctx context.Context, accountID, environment, expected string, fallback []ProjectReleaseMember, fences []BindingPromotionFence) (context.Context, error) {
	if len(fallback) == 0 || len(fallback) > api.ProjectReleaseSetMaxMembers || !api.ValidProjectEnvironmentSlug(environment) {
		return ctx, ErrInvalidArgument
	}
	id, err := uuid.Parse(expected)
	if err != nil || id == uuid.Nil || id.String() != expected {
		return ctx, ErrInvalidArgument
	}
	ctx, err = checkedReleaseGraphContext(ctx, accountID, environment, fallback, fences)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, checkedReleaseDeactivationKey{}, true), nil
}
func projectDeactivationReceipt(ctx context.Context, releaseID string, members []ProjectReleaseMember, now time.Time) *api.ProjectReleaseDeactivationReceipt {
	receipt := &api.ProjectReleaseDeactivationReceipt{ID: ctx.Value(checkedReleaseGraphKey{}).(string), ReleaseID: releaseID, CompletedAt: now, FallbackMembers: make([]api.ProjectReleaseSetMemberResponse, 0, len(members))}
	for _, member := range members {
		receipt.FallbackMembers = append(receipt.FallbackMembers, api.ProjectReleaseSetMemberResponse{AppID: uuid.MustParse(member.AppID).String(), DeploymentID: uuid.MustParse(member.DeploymentID).String()})
	}
	sort.Slice(receipt.FallbackMembers, func(i, j int) bool { return receipt.FallbackMembers[i].AppID < receipt.FallbackMembers[j].AppID })
	return receipt
}
