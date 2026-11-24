package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// bucketIDArgOffset is where `<app> <bucket-id>` start for subcommands that
// take a bucket ID right after the app: the subcommand and any nested verb.
func bucketIDArgOffset(args []string) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	// The tree rewrites before this runs, so today's spellings are listed too
	// (`bucket version list` arrives as `versions list`, `bucket version
	// delete` as `version-delete`). Missing them left `bucket version list APP
	// BUCKET-NAME` printing only its usage line (prod hunt r28).
	switch args[0] {
	case "upload", "download", "sign", "delete", "version-delete", "encryption-keys":
		return 1, true
	case "deletion", "deletions", "versioning", "writes", "tags", "version", "versions",
		"copy-source", "copy-sources", "cors", "encryption", "reconcile", "uploads", "lifecycle", "notifications":
		// `bucket <noun> <verb> <app> <bucket-id> …`
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			return 2, true
		}
	}
	return 0, false
}

// resolveBucketArgs replaces a bucket name in args with its ID. It only acts
// when the app and the bucket are the first two arguments after the verb, and
// leaves args untouched when there is nothing to resolve or no client.
func resolveBucketArgs(ctx context.Context, args []string, client func() (bucketLister, error)) ([]string, error) {
	offset, ok := bucketIDArgOffset(args)
	if !ok || len(args) < offset+2 {
		return args, nil
	}
	app, ref := args[offset], args[offset+1]
	if strings.HasPrefix(app, "-") || strings.HasPrefix(ref, "-") || !api.ValidAppSlug(app) {
		return args, nil
	}
	if id, err := uuid.Parse(ref); err == nil && id.String() == ref {
		return args, nil
	}
	c, err := client()
	if err != nil {
		return args, nil //nolint:nilerr // the command reports the login failure itself
	}
	id, err := resolveBucketRef(ctx, c, app, ref)
	if err != nil {
		return args, err
	}
	out := append([]string(nil), args...)
	out[offset+1] = id
	return out, nil
}

type bucketLister interface {
	ListObjectBuckets(ctx context.Context, app string) (api.ObjectBucketList, error)
}

// resolveBucketRef accepts a bucket's ID or its name. Bucket commands took
// only the UUID, and a name printed nothing but the usage line, although
// `gregale add bucket NAME` and `bucket list` speak in names (prod
// 2026-11-15 and 2026-11-19).
func resolveBucketRef(ctx context.Context, c bucketLister, app, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if id, err := uuid.Parse(ref); err == nil && id != uuid.Nil && id.String() == ref {
		return ref, nil
	}
	if ref == "" {
		return ref, nil
	}
	list, err := c.ListObjectBuckets(ctx, app)
	if err != nil {
		return "", fmt.Errorf("resolve bucket %q: %w", ref, err)
	}
	for _, bucket := range list.Items {
		if bucket.Name == ref {
			return bucket.ID, nil
		}
	}
	return "", fmt.Errorf("app %s has no bucket named %q; `gregale bucket list %s` shows its buckets", app, ref, app)
}
