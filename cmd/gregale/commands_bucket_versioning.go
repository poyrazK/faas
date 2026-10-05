package main

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type bucketVersioningClient interface {
	GetObjectBucketVersioning(context.Context, string, string) (api.ObjectBucketVersioning, error)
	PutObjectBucketVersioning(context.Context, string, string, string) (api.ObjectBucketVersioning, error)
}

func cmdBucketVersioning(args []string) int {
	if len(args) != 3 || !api.ValidAppSlug(args[1]) {
		return bucketVersioningUsage()
	}
	if _, err := uuid.Parse(args[2]); err != nil {
		return bucketVersioningUsage()
	}
	if args[0] != "status" && args[0] != "enable" && args[0] != "suspend" {
		return bucketVersioningUsage()
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	j, err := runBucketVersioning(context.Background(), c, args[0], args[1], args[2])
	if err != nil {
		return printErr("Could not configure bucket versioning", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(j))
	}
	_, _ = fmt.Fprintf(osStdout, "Versioning: desired %q, observed %q; %s (revision %d)\n", j.DesiredStatus, j.ObservedStatus, j.State, j.Revision)
	if j.State != "ready" {
		_, _ = fmt.Fprintln(osStdout, "Writes remain fenced until propagation and version inventory finish.")
	}
	if j.LastErrorCode != "" {
		_, _ = fmt.Fprintf(osStdout, "Reason: %s\n", j.LastErrorCode)
	}
	return 0
}
func runBucketVersioning(ctx context.Context, c bucketVersioningClient, action, app, bucket string) (api.ObjectBucketVersioning, error) {
	switch action {
	case "status":
		return c.GetObjectBucketVersioning(ctx, app, bucket)
	case "enable":
		return c.PutObjectBucketVersioning(ctx, app, bucket, "Enabled")
	case "suspend":
		return c.PutObjectBucketVersioning(ctx, app, bucket, "Suspended")
	default:
		return api.ObjectBucketVersioning{}, fmt.Errorf("invalid versioning action")
	}
}
func bucketVersioningUsage() int {
	PrintUsage(osStderr, "usage: gregale bucket versioning <status|enable|suspend> <app> <bucket-id>", "bucket")
	return 1
}
