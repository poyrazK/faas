package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const bucketVersionsUsage = "usage: gregale bucket versions list <app> <bucket-id> [--prefix PREFIX] [--delimiter DELIMITER] [--limit N] [--key-marker KEY] [--version-id-marker VERSION]"

func parseBucketVersions(args []string) (string, string, api.ObjectVersionListRequest, error) {
	req := api.ObjectVersionListRequest{Limit: api.MaxObjectS3ListItems}
	if len(args) < 3 || args[0] != "list" || !api.ValidAppSlug(args[1]) {
		return "", "", req, errors.New("invalid version list command")
	}
	b, err := uuid.Parse(args[2])
	if err != nil || b.String() != args[2] || b == uuid.Nil {
		return "", "", req, errors.New("invalid bucket")
	}
	fs := newFlagSet("bucket versions list", flag.ContinueOnError)
	setFlagOutput(fs, osStderr)
	fs.StringVar(&req.Prefix, "prefix", "", "key prefix")
	fs.StringVar(&req.Delimiter, "delimiter", "", "group keys by delimiter")
	fs.StringVar(&req.KeyMarker, "key-marker", "", "continuation key from the previous page")
	fs.StringVar(&req.VersionIDMarker, "version-id-marker", "", "public version continuation from the previous page")
	limit := int(req.Limit)
	fs.IntVar(&limit, "limit", limit, "maximum versions and common prefixes")
	if err := fs.Parse(args[3:]); err != nil {
		return "", "", req, err
	}
	if fs.NArg() != 0 || limit < 1 || limit > api.MaxObjectS3ListItems || !validBucketListText(req.Prefix) || !validBucketListText(req.KeyMarker) || !validBucketListText(req.Delimiter) || len(req.Delimiter) > api.MaxObjectS3DelimiterBytes || req.Delimiter != "" && utf8.RuneCountInString(req.Delimiter) != 1 || req.VersionIDMarker != "" && (req.KeyMarker == "" || req.VersionIDMarker != "null" && !validBucketImmutableVersion(req.VersionIDMarker)) {
		return "", "", req, errors.New("invalid version list options")
	}
	req.Limit = int32(limit)
	return args[1], args[2], req, nil
}

func cmdBucketVersionsList(args []string) int {
	app, bucket, req, err := parseBucketVersions(args)
	if err != nil {
		PrintUsage(osStderr, bucketVersionsUsage, "bucket")
		return 1
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := c.ListObjectBucketVersions(context.Background(), app, bucket, req)
	if err != nil {
		return printErr("Could not list object versions", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	for _, v := range out.Items {
		_, _ = fmt.Fprintf(osStdout, "%q\t%s\t%d bytes\tlatest=%t\tdelete-marker=%t\n", v.Key, v.VersionID, v.SizeBytes, v.IsLatest, v.DeleteMarker)
	}
	for _, p := range out.CommonPrefixes {
		_, _ = fmt.Fprintf(osStdout, "Prefix: %q\n", p)
	}
	if out.NextKeyMarker != "" {
		_, _ = fmt.Fprintf(osStdout, "Next key marker: %q\nNext version marker: %s\n", out.NextKeyMarker, out.NextVersionIDMarker)
	}
	return 0
}

func validBucketListText(s string) bool {
	if len(s) > api.MaxObjectS3ListTextBytes || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
