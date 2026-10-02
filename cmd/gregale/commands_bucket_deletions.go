package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdBucketDeletions(args []string) int {
	usage := "usage: gregale bucket deletions start <app> <bucket-id> <key> <request-id> [version-id|null] | status <app> <bucket-id> <request-id>"
	if len(args) < 4 || !api.ValidAppSlug(args[1]) {
		PrintUsage(osStderr, usage, "bucket")
		return 1
	}
	action, app, bucket := args[0], args[1], args[2]
	if action == "start" && len(args) != 5 && len(args) != 6 || action == "status" && len(args) != 4 || action != "start" && action != "status" {
		PrintUsage(osStderr, usage, "bucket")
		return 1
	}
	id := args[len(args)-1]
	selector := ""
	key := ""
	if action == "start" {
		key = args[3]
		id = args[4]
		if len(args) == 6 {
			selector = args[5]
			if !validDeletionVersionSelector(selector) {
				PrintUsage(osStderr, usage, "bucket")
				return 1
			}
		}
	}
	for _, value := range []string{bucket, id} {
		parsed, e := uuid.Parse(value)
		if e != nil || parsed.String() != value {
			PrintUsage(osStderr, usage, "bucket")
			return 1
		}
	}
	c, e := authedClient()
	if e != nil {
		return printErr("Not logged in", e)
	}
	var out api.ObjectDeletion
	if action == "start" {
		out, e = c.CreateObjectDeletion(context.Background(), app, bucket, api.ObjectDeletionRequest{ID: id, Key: key, VersionID: selector})
	} else {
		out, e = c.GetObjectDeletion(context.Background(), app, bucket, id)
	}
	if e != nil {
		return printErr("Could not process object deletion", e)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Deletion %s: %s", out.ID, out.State)
	if out.VersionID != "" {
		_, _ = fmt.Fprintf(osStdout, " (version %s)", out.VersionID)
	}
	_, _ = fmt.Fprintln(osStdout)
	return 0
}

func validDeletionVersionSelector(selector string) bool {
	if selector == "null" {
		return true
	}
	id, err := uuid.Parse(selector)
	return err == nil && id.String() == selector && id.Version() == 4 && id.Variant() == uuid.RFC4122
}
