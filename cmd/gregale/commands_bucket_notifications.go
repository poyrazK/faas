package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdBucketNotifications(args []string) int {
	if len(args) < 3 || args[0] != "get" && args[0] != "clear" && args[0] != "set" || !validBucketLifecycleArgs(args) {
		PrintUsage(osStderr, "usage: gregale bucket notifications <get|clear> <app> <bucket-id> | set <app> <bucket-id> <JSON-file|->", "bucket")
		return 1
	}
	in := api.ObjectBucketNotificationsRequest{}
	if args[0] == "set" {
		var err error
		in, err = readBucketNotificationsFile(args[len(args)-1])
		if err != nil {
			return printErr("Invalid notifications", err)
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := runBucketNotifications(context.Background(), c, args, in)
	if err != nil {
		return printErr("Could not process notifications", err)
	}
	return jsonOut(writeJSON(out))
}
func readBucketNotificationsFile(path string) (api.ObjectBucketNotificationsRequest, error) {
	var r io.Reader = os.Stdin
	if path != "-" {
		f, err := openCustomerFile(path)
		if err != nil {
			return api.ObjectBucketNotificationsRequest{}, err
		}
		defer f.Close() //nolint:errcheck
		r = f
	}
	return decodeBucketNotificationsFile(r)
}
func decodeBucketNotificationsFile(r io.Reader) (api.ObjectBucketNotificationsRequest, error) {
	var in api.ObjectBucketNotificationsRequest
	body, err := io.ReadAll(io.LimitReader(r, api.MaxObjectNotificationBodyBytes+1))
	if err != nil {
		return in, err
	}
	if int64(len(body)) > api.MaxObjectNotificationBodyBytes {
		return in, fmt.Errorf("notification configuration exceeds body limit")
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		return in, err
	}
	var extra any
	if in.Rules == nil || d.Decode(&extra) != io.EOF {
		return in, fmt.Errorf("expected one notification configuration with rules")
	}
	in.Rules, err = api.NormalizeObjectNotificationRules(in.Rules)
	return in, err
}

type bucketNotificationsClient interface {
	GetObjectBucketNotifications(context.Context, string, string) (api.ObjectBucketNotifications, error)
	PutObjectBucketNotifications(context.Context, string, string, api.ObjectBucketNotificationsRequest) (api.ObjectBucketNotifications, error)
	DeleteObjectBucketNotifications(context.Context, string, string) (api.ObjectBucketNotifications, error)
}

func runBucketNotifications(ctx context.Context, c bucketNotificationsClient, args []string, in api.ObjectBucketNotificationsRequest) (api.ObjectBucketNotifications, error) {
	switch args[0] {
	case "get":
		return c.GetObjectBucketNotifications(ctx, args[1], args[2])
	case "set":
		return c.PutObjectBucketNotifications(ctx, args[1], args[2], in)
	case "clear":
		return c.DeleteObjectBucketNotifications(ctx, args[1], args[2])
	}
	return api.ObjectBucketNotifications{}, fmt.Errorf("unknown notification action")
}
