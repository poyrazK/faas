package state

import (
	"context"
	"time"
)

// RollbackOutcomeCursor lets a bounded sweep visit newer outcomes even while
// older targets are still preparing or temporarily unreadable.
type RollbackOutcomeCursor struct {
	RequestedAt time.Time
	ID          string
}
type rollbackOutcomeCursorKey struct{}

func WithRollbackOutcomeCursor(ctx context.Context, cursor RollbackOutcomeCursor) context.Context {
	return context.WithValue(ctx, rollbackOutcomeCursorKey{}, cursor)
}
func rollbackOutcomeCursor(ctx context.Context) RollbackOutcomeCursor {
	cursor, _ := ctx.Value(rollbackOutcomeCursorKey{}).(RollbackOutcomeCursor)
	return cursor
}

// RollbackCompletedAt recognizes completion even if a newer deployment has
// already replaced the target. The grace covers admission recording just after
// a very fast activation, matching the outcome worker's existing semantics.
func RollbackCompletedAt(requested time.Time, target Deployment) (time.Time, bool) {
	if done := target.RolloutCompletedAt; done != nil && !done.Before(requested.Add(-time.Second)) &&
		(target.Status == DeployLive || target.Status == DeploySuperseded) {
		return *done, true
	}
	return time.Time{}, false
}
