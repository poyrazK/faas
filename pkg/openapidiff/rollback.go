package openapidiff

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// CheckDeploymentRollback pins the baseline to the selected serving deployment.
// A dark candidate or another production lane cannot replace that baseline.
// Pre-capture targets retain the existing projection fallback.
func CheckDeploymentRollback(ctx context.Context, store state.Store, appID, targetID, currentID, scope string) (PromotionCheck, error) {
	snapshots, ok := store.(state.OpenAPISnapshotStore)
	if !ok {
		return PromotionCheck{}, ErrSnapshotStoreUnavailable
	}
	proposed, err := snapshots.OpenAPISnapshotByDeployment(ctx, targetID)
	source := ""
	if errors.Is(err, state.ErrNotFound) {
		projected, projectionErr := CheckPromotion(ctx, store, appID, targetID, scope)
		if projectionErr != nil && !errors.Is(projectionErr, ErrSnapshotBaselineMissing) {
			return projected, projectionErr
		}
		proposed, source, err = projected.Proposed, projected.ProposedSource, nil
	}
	if err != nil {
		return PromotionCheck{}, fmt.Errorf("read rollback contract: %w", err)
	}
	check := PromotionCheck{Proposed: proposed, ProposedSource: source, Diff: SnapshotDiff{ProposedSHA256: proposed.SHA256}}
	if currentID == "" {
		return check, ErrSnapshotBaselineMissing
	}
	baseline, err := snapshots.OpenAPISnapshotByDeployment(ctx, currentID)
	if errors.Is(err, state.ErrNotFound) {
		return check, ErrSnapshotBaselineMissing
	}
	if err != nil {
		return check, fmt.Errorf("read selected serving contract: %w", err)
	}
	normalize := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			return "default"
		}
		return value
	}
	if baseline.AppID != appID || proposed.AppID != appID || normalize(baseline.Scope) != normalize(scope) || normalize(proposed.Scope) != normalize(scope) {
		return check, fmt.Errorf("rollback contract snapshot identity mismatch")
	}
	check.Baseline, check.HasBaseline = baseline, true
	check.Diff, err = CompareSnapshots(baseline.Snapshot, proposed.Snapshot)
	return check, err
}
