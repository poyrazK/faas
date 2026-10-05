package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdBucketLifecycle(args []string) int {
	if !validBucketLifecycleArgs(args) {
		PrintUsage(osStderr, "usage: gregale bucket lifecycle <get|clear|scan> <app> <bucket-id> | set <app> <bucket-id> <JSON-file|-> | status <app> <bucket-id> <scan-id>", "bucket")
		return 1
	}
	var in api.ObjectBucketLifecycleRequest
	if args[0] == "set" {
		var err error
		in, err = readBucketLifecycleFile(args[3])
		if err != nil {
			return printErr("Invalid lifecycle configuration", err)
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := runBucketLifecycle(context.Background(), c, args, in)
	if err != nil {
		return printErr("Could not process lifecycle policy", err)
	}
	// Configuration and scan progress are structured in both output modes.
	return jsonOut(writeJSON(out))
}

func validBucketLifecycleArgs(args []string) bool {
	if len(args) < 3 || !api.ValidAppSlug(args[1]) {
		return false
	}
	if id, err := uuid.Parse(args[2]); err != nil || id.String() != args[2] {
		return false
	}
	switch args[0] {
	case "get", "clear", "scan":
		return len(args) == 3
	case "set":
		return len(args) == 4 && args[3] != ""
	case "status":
		if len(args) != 4 {
			return false
		}
		id, err := uuid.Parse(args[3])
		return err == nil && id.String() == args[3]
	}
	return false
}

func readBucketLifecycleFile(path string) (api.ObjectBucketLifecycleRequest, error) {
	var r io.Reader = os.Stdin
	if path != "-" {
		f, err := openCustomerFile(path)
		if err != nil {
			return api.ObjectBucketLifecycleRequest{}, err
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	return decodeBucketLifecycleFile(r)
}
func decodeBucketLifecycleFile(r io.Reader) (api.ObjectBucketLifecycleRequest, error) {
	var in api.ObjectBucketLifecycleRequest
	body, err := io.ReadAll(io.LimitReader(r, api.MaxObjectLifecycleBodyBytes+1))
	if err != nil {
		return in, err
	}
	if int64(len(body)) > api.MaxObjectLifecycleBodyBytes {
		return in, fmt.Errorf("lifecycle document exceeds %d bytes", api.MaxObjectLifecycleBodyBytes)
	}
	if err = decodeStrictLifecycleJSON(body, &in); err != nil {
		return in, err
	}
	if len(in.Rules) == 0 {
		return in, fmt.Errorf("rules must be nonempty; use clear to remove the policy")
	}
	in.Rules, err = api.NormalizeObjectLifecycleRules(in.Rules)
	return in, err
}

type bucketLifecycleClient interface {
	GetObjectBucketLifecycle(context.Context, string, string) (api.ObjectBucketLifecycle, error)
	PutObjectBucketLifecycle(context.Context, string, string, api.ObjectBucketLifecycleRequest) (api.ObjectBucketLifecycle, error)
	DeleteObjectBucketLifecycle(context.Context, string, string) (api.ObjectBucketLifecycle, error)
	CreateObjectLifecycleScan(context.Context, string, string) (api.ObjectLifecycleScan, error)
	GetObjectLifecycleScan(context.Context, string, string, string) (api.ObjectLifecycleScan, error)
}

func runBucketLifecycle(ctx context.Context, c bucketLifecycleClient, args []string, in api.ObjectBucketLifecycleRequest) (any, error) {
	switch args[0] {
	case "get":
		return c.GetObjectBucketLifecycle(ctx, args[1], args[2])
	case "set":
		return c.PutObjectBucketLifecycle(ctx, args[1], args[2], in)
	case "clear":
		return c.DeleteObjectBucketLifecycle(ctx, args[1], args[2])
	case "scan":
		return c.CreateObjectLifecycleScan(ctx, args[1], args[2])
	case "status":
		return c.GetObjectLifecycleScan(ctx, args[1], args[2], args[3])
	default:
		return nil, fmt.Errorf("unknown lifecycle action")
	}
}

func decodeStrictLifecycleJSON(body []byte, in *api.ObjectBucketLifecycleRequest) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(in); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("expected one lifecycle configuration")
	}
	return nil
}
