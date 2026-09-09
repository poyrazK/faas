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
	if !api.ApiContractDiffEnabled() || !strings.EqualFold(strings.TrimSpace(dep.Scope), "prod") {
		return nil
	}
	check, err := openapidiff.CheckPromotion(ctx, h.store, dep.AppID, dep.ID, "prod")
	if err != nil {
		if errors.Is(err, openapidiff.ErrSnapshotBaselineMissing) {
			return nil
		}
		return fmt.Errorf("api contract check: %w", err)
	}
	if len(check.Diff.Breaks) == 0 {
		return nil
	}
	return &openapidiff.GateError{Diff: check.Diff}
}
