package main

import (
	"context"
	"flag"
	"fmt"
	"text/tabwriter"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// Every `bucket …` subcommand takes a bucket ID, but nothing in the CLI
// listed an app's buckets or deleted one: the only way to learn an ID was to
// re-run `add bucket --json` (production-us hunt #9, H9-141).

type bucketCatalogClient interface {
	ListObjectBuckets(context.Context, string) (api.ObjectBucketList, error)
	DeleteObjectBucket(context.Context, string, string) error
}

const (
	bucketListUsage   = "usage: gregale bucket list <app>"
	bucketDeleteUsage = "usage: gregale bucket delete <app> <bucket-id> [--yes]"
)

func cmdBucketList(args []string) int {
	if len(args) != 1 || !api.ValidAppSlug(args[0]) {
		PrintUsage(osStderr, bucketListUsage, "bucket")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	return runBucketList(context.Background(), client, args[0])
}

func runBucketList(ctx context.Context, client bucketCatalogClient, app string) int {
	list, err := client.ListObjectBuckets(ctx, app)
	if err != nil {
		return printErr("Could not list buckets", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(list))
	}
	renderBucketList(app, list)
	return 0
}

func renderBucketList(app string, list api.ObjectBucketList) {
	if len(list.Items) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No object-storage buckets for %s.\n", app)
	} else {
		tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "BUCKET ID\tNAME\tSCOPE\tREGION\tSTATE\tPUBLIC AT")
		for _, bucket := range list.Items {
			publicAt := "-"
			if bucket.Public {
				publicAt = bucket.ServeAt
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				bucket.ID, bucket.Name, bucket.Scope, bucket.Region, bucket.State, publicAt)
		}
		_ = tw.Flush()
	}
	if !list.Enabled {
		_, _ = fmt.Fprintln(osStdout, "Object storage is not enabled on this installation; existing buckets can still be emptied and deleted.")
	}
}

func cmdBucketDelete(args []string) int {
	flags, positional := splitArgsForFlags(args, "quiet", "yes")
	args = append(flags, positional...)
	fs := newFlagSet("bucket delete", flag.ContinueOnError)
	quiet := fs.Bool("quiet", false, "skip the typed confirmation (for scripts)")
	fs.BoolVar(quiet, "yes", false, "confirm deletion without prompting")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 2 || !api.ValidAppSlug(fs.Arg(0)) {
		PrintUsage(osStderr, bucketDeleteUsage, "bucket")
		return 1
	}
	if _, err := uuid.Parse(fs.Arg(1)); err != nil {
		PrintUsage(osStderr, bucketDeleteUsage, "bucket")
		return 1
	}
	if code := requireAutomationConfirmation(*quiet, "--yes (or --quiet)"); code != 0 {
		return code
	}
	if !*quiet {
		_, _ = fmt.Fprintf(osStderr, "About to delete bucket %s of %s. It must be empty, including retained versions.\n", fs.Arg(1), fs.Arg(0))
		if !requireTyped("delete bucket") {
			return 1
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	return runBucketDelete(context.Background(), client, fs.Arg(0), fs.Arg(1))
}

func runBucketDelete(ctx context.Context, client bucketCatalogClient, app, bucketID string) int {
	if err := client.DeleteObjectBucket(ctx, app, bucketID); err != nil {
		return printErr("Could not delete bucket", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]string{"app": app, "bucket_id": bucketID, "status": "deleted"}))
	}
	PrintOK(osStdout, "Bucket %s deleted.", bucketID)
	return 0
}
