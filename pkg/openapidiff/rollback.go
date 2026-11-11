package openapidiff

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// ErrRollbackTargetUndeclared means the rollback or traffic target was captured
// before the app declared any API contract: its snapshot has no paths. Such a
// target never promised the routes it lacks, so comparing it with today's
// contract would report every route as removed and block incident recovery to
// any pre-adoption revision (ChronoLake finding 24). It wraps
// ErrSnapshotBaselineMissing, which every caller already treats as "nothing to
// compare, allowed".
var ErrRollbackTargetUndeclared = fmt.Errorf("%w: rollback target declared no API contract", ErrSnapshotBaselineMissing)

// undeclaredContract reports whether a stored snapshot declares no paths. An
// unreadable snapshot is not undeclared: CompareSnapshots reports it.
func undeclaredContract(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	spec, err := UnmarshalSnapshot(raw)
	return err == nil && len(spec.Paths) == 0
}

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
	if undeclaredContract(proposed.Snapshot) {
		return check, ErrRollbackTargetUndeclared
	}
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
