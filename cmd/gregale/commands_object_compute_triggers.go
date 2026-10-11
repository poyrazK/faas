package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

type manifestComputeTriggerClient interface {
	GetObjectBucketNotifications(context.Context, string, string) (api.ObjectBucketNotifications, error)
	ReconcileObjectStorageComputeTriggers(context.Context, string, string, api.ReconcileObjectStorageComputeTriggersRequest) (api.ObjectBucketNotifications, error)
}

func reconcileManifestComputeTriggers(ctx context.Context, client manifestObjectStorageClient, slug string, plan manifestObjectStorageBindingPlan) error {
	if plan.dependency.Triggers == nil {
		return nil
	}
	st, ok := client.(manifestComputeTriggerClient)
	if !ok {
		return fmt.Errorf("compute trigger reconciliation is unavailable")
	}
	current, err := st.GetObjectBucketNotifications(ctx, slug, plan.bucket.ID)
	if err != nil {
		return fmt.Errorf("bucket %q: read trigger revision: %w", plan.bucket.Name, err)
	}
	_, err = st.ReconcileObjectStorageComputeTriggers(ctx, slug, plan.bucket.ID, api.ReconcileObjectStorageComputeTriggersRequest{ExpectedRevision: &current.Revision, Triggers: *plan.dependency.Triggers})
	if err != nil {
		return fmt.Errorf("bucket %q: reconcile compute triggers: %w", plan.bucket.Name, err)
	}
	return nil
}
