package state

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrRollbackCrossesRelease = errors.New("state: rollback crosses an unacknowledged release")
var ErrRollbackTargetDatabaseRestored = errors.New("state: rollback target release was undone by a database restore")

type checkedRollbackReleaseReviewKey struct{}

// WithCheckedRollbackReleaseReview records the exact started release runs the
// caller acknowledged. It grants no acknowledgement for a later run or retry.
// Stores persist this internal metadata with intent; public receipts omit it.
func WithCheckedRollbackReleaseReview(ctx context.Context, runs []ReleaseRun) context.Context {
	return context.WithValue(ctx, checkedRollbackReleaseReviewKey{}, slices.Clone(runs))
}
func checkedRollbackReleaseReview(ctx context.Context) []ReleaseRun {
	runs, _ := ctx.Value(checkedRollbackReleaseReviewKey{}).([]ReleaseRun)
	return runs
}

type checkedRollbackReceipt struct {
	api.RollbackOperation
	ReleaseReview []ReleaseRun `json:"_release_review,omitempty"`
}

func marshalCheckedRollback(ctx context.Context, r api.RollbackOperation) ([]byte, error) {
	return json.Marshal(checkedRollbackReceipt{RollbackOperation: r, ReleaseReview: checkedRollbackReleaseReview(ctx)})
}
func readCheckedRollbackReleaseReview(raw []byte) ([]ReleaseRun, error) {
	var receipt checkedRollbackReceipt
	err := json.Unmarshal(raw, &receipt)
	return receipt.ReleaseReview, err
}
func checkRollbackReleaseReview(target string, runs []ReleaseRun, undone []string, review []ReleaseRun) error {
	if slices.Contains(undone, target) {
		return ErrRollbackTargetDatabaseRestored
	}
	for _, run := range runs {
		if slices.Contains(undone, run.DeploymentID) {
			continue
		}
		found := slices.ContainsFunc(review, func(ack ReleaseRun) bool {
			return ack.DeploymentID == run.DeploymentID && ack.StartedAt.Equal(run.StartedAt)
		})
		if !found {
			return ErrRollbackCrossesRelease
		}
	}
	return nil
}
func checkedRollbackCrossedReleaseIDs(ctx context.Context) []string {
	var ids []string
	for _, run := range checkedRollbackReleaseReview(ctx) {
		if !slices.Contains(ids, run.DeploymentID) {
			ids = append(ids, run.DeploymentID)
		}
	}
	return ids
}
