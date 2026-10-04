package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type bucketCopySourceClient interface {
	ListObjectS3CopySources(context.Context, string, string, string) (api.ObjectS3CopySourceList, error)
	SetObjectS3CopySource(context.Context, string, string, string, string, api.SetObjectS3CopySourceRequest) (api.ObjectS3CopySource, error)
	DeleteObjectS3CopySource(context.Context, string, string, string, string) error
}

func validBucketCopySourceArgs(args []string) bool {
	if len(args) < 4 || !api.ValidAppSlug(args[1]) {
		return false
	}
	switch args[0] {
	case "list":
		if len(args) != 4 {
			return false
		}
	case "grant":
		if len(args) != 5 && len(args) != 6 {
			return false
		}
	case "revoke":
		if len(args) != 5 {
			return false
		}
	default:
		return false
	}
	for _, id := range args[2:min(len(args), 5)] {
		p, err := uuid.Parse(id)
		if err != nil || p == uuid.Nil || p.String() != id {
			return false
		}
	}
	return true
}
func cmdBucketCopySources(args []string) int {
	if !validBucketCopySourceArgs(args) {
		PrintUsage(osStderr, "usage: gregale bucket copy-sources <list|grant|revoke> <app> <bucket-id> <credential-id> [source-bucket-id] [prefix]", "bucket")
		return 1
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := runBucketCopySources(context.Background(), c, args)
	if err != nil {
		return printErr("Could not manage copy sources", err)
	}
	return jsonOut(writeJSON(out))
}
func runBucketCopySources(ctx context.Context, c bucketCopySourceClient, args []string) (any, error) {
	if !validBucketCopySourceArgs(args) {
		return nil, fmt.Errorf("invalid copy source arguments")
	}
	switch args[0] {
	case "list":
		return c.ListObjectS3CopySources(ctx, args[1], args[2], args[3])
	case "revoke":
		err := c.DeleteObjectS3CopySource(ctx, args[1], args[2], args[3], args[4])
		return map[string]bool{"revoked": err == nil}, err
	default:
		prefix := ""
		if len(args) == 6 {
			prefix = args[5]
		}
		return c.SetObjectS3CopySource(ctx, args[1], args[2], args[3], args[4], api.SetObjectS3CopySourceRequest{Prefix: prefix})
	}
}
