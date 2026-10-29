package main

// adr: 20261025202805 — `gregale bucket cors` manages a bucket's CORS rules.
// The bucket-cors capability owns the command path, so help and completion
// hide it until the capability is released for the account.

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

const bucketCORSUsage = "usage: gregale bucket cors <get|clear> <app> <bucket-id> | set <app> <bucket-id> <JSON-file|->"

func cmdBucketCORS(args []string) int {
	if !validBucketCORSArgs(args) {
		PrintUsage(osStderr, bucketCORSUsage, "bucket")
		return 1
	}
	var rules []api.ObjectBucketCORSRule
	if args[0] == "set" {
		var err error
		if rules, err = readBucketCORSFile(args[3]); err != nil {
			return printErr("Invalid CORS configuration", err)
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := runBucketCORS(context.Background(), c, args, rules)
	if err != nil {
		return printErr("Could not process bucket CORS rules", err)
	}
	return jsonOut(writeJSON(out))
}

func validBucketCORSArgs(args []string) bool {
	if len(args) < 3 || !api.ValidAppSlug(args[1]) {
		return false
	}
	if id, err := uuid.Parse(args[2]); err != nil || id.String() != args[2] {
		return false
	}
	switch args[0] {
	case "get", "clear":
		return len(args) == 3
	case "set":
		return len(args) == 4 && args[3] != ""
	}
	return false
}

func readBucketCORSFile(path string) ([]api.ObjectBucketCORSRule, error) {
	var r io.Reader = os.Stdin
	if path != "-" {
		f, err := openCustomerFile(path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	return decodeBucketCORSFile(r)
}

// decodeBucketCORSFile reads {"rules": [...]} with the server's bounds, so a
// malformed file fails before any request.
func decodeBucketCORSFile(r io.Reader) ([]api.ObjectBucketCORSRule, error) {
	body, err := io.ReadAll(io.LimitReader(r, api.MaxObjectBucketCORSBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > api.MaxObjectBucketCORSBodyBytes {
		return nil, fmt.Errorf("CORS document exceeds %d bytes", api.MaxObjectBucketCORSBodyBytes)
	}
	var in api.ObjectBucketCORSRequest
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err = d.Decode(&in); err != nil {
		return nil, err
	}
	if d.More() {
		return nil, fmt.Errorf("CORS document has trailing data")
	}
	if !api.ValidObjectBucketCORSRules(in.Rules) {
		return nil, fmt.Errorf("rules must hold 1 to %d valid rules; use clear to remove them", api.MaxObjectBucketCORSRules)
	}
	return in.Rules, nil
}

type bucketCORSClient interface {
	GetObjectBucketCORS(context.Context, string, string) (api.ObjectBucketCORS, error)
	PutObjectBucketCORS(context.Context, string, string, []api.ObjectBucketCORSRule) (api.ObjectBucketCORS, error)
	DeleteObjectBucketCORS(context.Context, string, string) error
}

func runBucketCORS(ctx context.Context, c bucketCORSClient, args []string, rules []api.ObjectBucketCORSRule) (api.ObjectBucketCORS, error) {
	switch args[0] {
	case "set":
		return c.PutObjectBucketCORS(ctx, args[1], args[2], rules)
	case "clear":
		if err := c.DeleteObjectBucketCORS(ctx, args[1], args[2]); err != nil {
			return api.ObjectBucketCORS{}, err
		}
		return api.ObjectBucketCORS{BucketID: args[2], Rules: []api.ObjectBucketCORSRule{}}, nil
	default:
		return c.GetObjectBucketCORS(ctx, args[1], args[2])
	}
}
