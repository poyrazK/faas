package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdBucketTags(args []string) int {
	usage := "usage: gregale bucket tags <get|clear> <app> <bucket-id> <key> [version-id|null] | set <app> <bucket-id> <key> <URL-encoded-tags> [version-id|null]"
	if len(args) < 4 || !api.ValidAppSlug(args[1]) {
		PrintUsage(osStderr, usage, "bucket")
		return 1
	}
	action, app, bucket, key := args[0], args[1], args[2], args[3]
	count := 4
	if action == "set" {
		count++
	}
	id, err := uuid.Parse(bucket)
	if err != nil || id.String() != bucket || action != "get" && action != "set" && action != "clear" || len(args) != count && len(args) != count+1 {
		PrintUsage(osStderr, usage, "bucket")
		return 1
	}
	selector := ""
	if len(args) == count+1 {
		selector = args[count]
		if !validDeletionVersionSelector(selector) {
			PrintUsage(osStderr, usage, "bucket")
			return 1
		}
	}
	tags := map[string]string{}
	if action == "set" {
		tags, err = parseBucketTags(args[4])
		if err != nil {
			return printErr("Invalid tags", err)
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.ObjectTaggingResult
	switch action {
	case "get":
		out, err = c.GetObjectBucketTags(context.Background(), app, bucket, key, selector)
	case "set":
		out, err = c.PutObjectBucketTags(context.Background(), app, bucket, key, selector, api.ObjectTaggingRequest{Tags: tags})
	case "clear":
		out, err = c.DeleteObjectBucketTags(context.Background(), app, bucket, key, selector)
	}
	if err != nil {
		return printErr("Could not process object tags", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Tags for %s", key)
	if out.VersionID != "" {
		_, _ = fmt.Fprintf(osStdout, " (version %s)", out.VersionID)
	}
	_, _ = fmt.Fprintf(osStdout, ": %s\n", encodeBucketTags(out.Tags))
	return 0
}

func parseBucketTags(raw string) (map[string]string, error) {
	if raw != "" {
		for _, pair := range strings.Split(raw, "&") {
			if pair == "" || !strings.Contains(pair, "=") {
				return nil, fmt.Errorf("tags require key=value pairs")
			}
		}
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, err
	}
	tags := make(map[string]string, len(values))
	for key, value := range values {
		if key == "" || len(value) != 1 {
			return nil, fmt.Errorf("tags require unique nonempty keys and key=value pairs")
		}
		tags[key] = value[0]
	}
	return tags, nil
}

func encodeBucketTags(tags map[string]string) string {
	values := make(url.Values, len(tags))
	for key, value := range tags {
		values.Set(key, value)
	}
	return values.Encode()
}
