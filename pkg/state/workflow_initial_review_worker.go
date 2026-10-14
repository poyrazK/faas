package state

import (
	"context"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
)

func initialPolicyPreparationEnabled(p api.WorkflowRecoveryPolicy) bool {
	return p.Enabled && (p.Mode == "preview" || p.Mode == "automatic_initial") && recoveryPolicyHas(p.Triggers, "initial_failure")
}
func initialPolicyNextCursor(ids []string) string {
	if len(ids) == api.WorkflowRecoveryPolicyJobsPerPoll {
		return ids[len(ids)-1]
	}
	return ""
}

// Preparation runs after releasing the policy claim: mutations acquire account
// authority before policy authority, and recheck the claimed version themselves.
func prepareInitialPolicyReviews(ctx context.Context, store WorkflowInitialRecoveryReviewStore, account, app string, version int64, ids []string) error {
	ctx = WithEventRecoveryActor(ctx, "system", "initial_recovery_policy")
	for _, id := range ids {
		_, err := store.PrepareWorkflowInitialRecoveryReview(ctx, account, app, id, api.WorkflowInitialRecoveryReviewRequest{ExpectedPolicyVersion: version})
		if errors.Is(err, ErrWorkflowRecoveryPolicyConflict) {
			return nil
		}
		if errors.Is(err, ErrWorkflowInitialReviewBlocked) || errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("prepare initial policy review: %w", err)
		}
	}
	return nil
}
