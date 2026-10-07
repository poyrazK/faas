package main

import (
	"context"
	"errors"
	"flag"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type bucketUploadsClient interface {
	ListObjectMultipartUploads(context.Context, string, string, int, string) (api.ObjectMultipartUploadList, error)
	GetObjectMultipartUpload(context.Context, string, string, string) (api.ObjectMultipartUpload, error)
	ListObjectMultipartParts(context.Context, string, string, string, int, int) (api.ObjectMultipartPartList, error)
}

type bucketUploadsOptions struct {
	action, app, bucket, upload, cursor string
	limit, marker                       int
}

func parseBucketUploads(args []string) (bucketUploadsOptions, error) {
	o := bucketUploadsOptions{}
	if len(args) < 3 || !api.ValidAppSlug(args[1]) {
		return o, errors.New("invalid multipart command")
	}
	o.action, o.app, o.bucket = args[0], args[1], args[2]
	if o.bucket == "" {
		return o, errors.New("bucket ID required")
	}
	pos := 3
	if o.action == "status" || o.action == "parts" {
		if len(args) < 4 || args[3] == "" {
			return o, errors.New("upload ID required")
		}
		o.upload, pos = args[3], 4
	} else if o.action != "list" {
		return o, errors.New("invalid multipart command")
	}
	for _, value := range []string{o.bucket, o.upload} {
		if value == "" {
			continue
		}
		if id, err := uuid.Parse(value); err != nil || id == uuid.Nil || id.String() != value {
			return o, errors.New("invalid owned ID")
		}
	}
	fs := newFlagSet("bucket uploads "+o.action, flag.ContinueOnError)
	setFlagOutput(fs, osStderr)
	if o.action != "status" {
		fs.IntVar(&o.limit, "limit", 100, "page size (1..1000)")
	}
	switch o.action {
	case "list":
		fs.StringVar(&o.cursor, "cursor", "", "next page cursor")
	case "parts":
		fs.IntVar(&o.marker, "part-number-marker", 0, "last part from the previous page")
	}
	if err := fs.Parse(args[pos:]); err != nil {
		return o, err
	}
	if fs.NArg() != 0 || o.action != "status" && (o.limit < 1 || o.limit > api.MaxObjectS3ListItems) || o.marker < 0 || o.marker > api.MaxMultipartParts {
		return o, errors.New("invalid page options")
	}
	return o, nil
}

func cmdBucketUploads(args []string) int {
	o, err := parseBucketUploads(args)
	if err != nil {
		PrintUsage(osStderr, "usage: gregale bucket uploads list <app> <bucket-id> [--limit N] [--cursor ID] | gregale bucket uploads <status|parts> <app> <bucket-id> <upload-id>", "bucket")
		return 1
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := runBucketUploads(context.Background(), c, o)
	if err != nil {
		return printErr("Could not inspect multipart uploads", err)
	}
	return jsonOut(writeJSON(out))
}

func runBucketUploads(ctx context.Context, c bucketUploadsClient, o bucketUploadsOptions) (any, error) {
	switch o.action {
	case "list":
		return c.ListObjectMultipartUploads(ctx, o.app, o.bucket, o.limit, o.cursor)
	case "status":
		return c.GetObjectMultipartUpload(ctx, o.app, o.bucket, o.upload)
	case "parts":
		return c.ListObjectMultipartParts(ctx, o.app, o.bucket, o.upload, o.marker, o.limit)
	default:
		return nil, errors.New("invalid multipart command")
	}
}
