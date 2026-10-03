package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdBucket(args []string) int {
	if len(args) > 0 && args[0] == "encryption-keys" {
		return cmdBucketEncryptionKeys(args[1:])
	}
	if len(args) > 0 && args[0] == "notifications" {
		return cmdBucketNotifications(args[1:])
	}
	if len(args) > 0 && args[0] == "lifecycle" {
		return cmdBucketLifecycle(args[1:])
	}
	if len(args) > 0 && args[0] == "tags" {
		return cmdBucketTags(args[1:])
	}
	if len(args) > 0 && args[0] == "deletions" {
		return cmdBucketDeletions(args[1:])
	}
	if len(args) > 0 && args[0] == "version-delete" {
		return cmdBucketVersionDelete(args[1:])
	}
	if len(args) > 0 && args[0] == "versioning" {
		return cmdBucketVersioning(args[1:])
	}
	if len(args) > 0 && args[0] == "writes" {
		return cmdBucketWrites(args[1:])
	}
	return cmdBucketCapacity(args)
}

func cmdBucketCapacity(args []string) int {
	if len(args) < 4 || args[0] != "reconcile" || !api.ValidAppSlug(args[2]) {
		return bucketCapacityUsage()
	}
	action, app, bucket := args[1], args[2], args[3]
	if _, err := uuid.Parse(bucket); err != nil {
		return bucketCapacityUsage()
	}
	if action == "start" && len(args) != 4 || action != "start" && (action != "status" && action != "cancel" || len(args) != 5) {
		return bucketCapacityUsage()
	}
	if len(args) == 5 {
		if _, err := uuid.Parse(args[4]); err != nil {
			return bucketCapacityUsage()
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	job, err := runBucketCapacity(context.Background(), client, action, app, bucket, args[4:])
	if err != nil {
		return printErr("Could not reconcile bucket capacity", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(job))
	}
	_, _ = fmt.Fprintf(osStdout, "Reconciliation %s: %s\nReserved capacity: %d → %d bytes; %d → %d keys\nReclaimed: %d bytes, %d keys\n", job.ID, job.State, job.BeforeBytes, job.AfterBytes, job.BeforeKeys, job.AfterKeys, job.ReclaimedBytes, job.ReclaimedKeys)
	if job.InventoryScope == "all_versions" {
		_, _ = fmt.Fprintf(osStdout, "Version inventory: %d pages, %d retained entries, %d bytes\n", job.ScannedPages, job.ScannedVersions, job.ScannedBytes)
	}
	if job.LastErrorCode != "" {
		_, _ = fmt.Fprintf(osStdout, "Reason: %s; pending writes: %d\n", job.LastErrorCode, job.PendingWrites)
	}
	return 0
}
func bucketCapacityUsage() int {
	PrintUsage(osStderr, "usage: gregale bucket reconcile start <app> <bucket-id> | gregale bucket reconcile <status|cancel> <app> <bucket-id> <job-id>", "bucket")
	return 1
}

type bucketCapacityClient interface {
	CreateObjectCapacityReconciliation(context.Context, string, string) (api.ObjectCapacityReconciliation, error)
	GetObjectCapacityReconciliation(context.Context, string, string, string) (api.ObjectCapacityReconciliation, error)
	CancelObjectCapacityReconciliation(context.Context, string, string, string) (api.ObjectCapacityReconciliation, error)
}

func runBucketCapacity(ctx context.Context, c bucketCapacityClient, action, app, bucket string, ids []string) (api.ObjectCapacityReconciliation, error) {
	switch action {
	case "start":
		return c.CreateObjectCapacityReconciliation(ctx, app, bucket)
	case "status":
		return c.GetObjectCapacityReconciliation(ctx, app, bucket, ids[0])
	default:
		return c.CancelObjectCapacityReconciliation(ctx, app, bucket, ids[0])
	}
}
