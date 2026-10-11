package imaged

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

// checkAPIContract is the compute-side enforcement point. The snapshot is
// projected before MarkDeploymentLive, so a rejected production deployment
// can never become routable. Non-production scopes and the default-off flag
// retain the existing advisory behavior.
func (h *Handler) checkAPIContract(ctx context.Context, dep state.Deployment) error {
	_, err := h.checkAPIContractContext(ctx, dep)
	return err
}
func (h *Handler) checkAPIContractContext(ctx context.Context, dep state.Deployment) (context.Context, error) {
	if !api.ApiContractDiffEnabled() || !strings.EqualFold(strings.TrimSpace(dep.Scope), "prod") {
		return ctx, nil
	}
	check, err := openapidiff.CheckLiveContract(ctx, h.store, dep.AppID, dep.ID, "prod", false)
	if err != nil {
		if errors.Is(err, openapidiff.ErrSnapshotBaselineMissing) {
			return ctx, nil
		}
		return ctx, fmt.Errorf("api contract check: %w", err)
	}
	if check.HasBaseline {
		var fence *state.RouteRemovalFence
		check, fence, err = openapidiff.ApplyRemovalException(ctx, h.store, check, dep.TrafficPercentExplicit && dep.TrafficPercent == 0)
		if err != nil {
			return ctx, err
		}
		if fence != nil {
			ctx = state.WithRouteRemovalFence(ctx, *fence)
		}
	}
	if !check.Diff.Blocking() {
		return ctx, nil
	}
	return ctx, &openapidiff.GateError{Diff: check.Diff}
}
