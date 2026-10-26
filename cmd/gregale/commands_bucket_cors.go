package main

// `gregale bucket cors` — ADR-20261025202805: per-bucket CORS rules that let a
// customer's own sites use the bucket's signed URLs and upload grants. The
// bucket-cors capability gates the command like its API routes.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// maxBucketCORSFileBytes bounds a rules file: 100 rules of short S3 lists.
const maxBucketCORSFileBytes = 256 << 10

const bucketCORSUsage = "usage: gregale bucket cors <get|delete> <app> <bucket-id> | set <app> <bucket-id> <JSON-file|->"

func cmdBucketCORS(args []string) int {
	if len(args) > 0 && args[0] == "clear" {
		args = append([]string{"delete"}, args[1:]...)
	}
	if !validBucketCORSArgs(args) {
		PrintUsage(osStderr, bucketCORSUsage, "bucket")
		return 1
	}
	var rules []api.ObjectBucketCORSRule
	if args[0] == "set" {
		var err error
		rules, err = readBucketCORSFile(args[3])
		if err != nil {
			return printErr("Invalid CORS rules", err)
		}
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := runBucketCORS(context.Background(), c, args, rules)
	if err != nil {
		return printErr("Could not process bucket CORS", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	printBucketCORS(osStdout, args[0], out)
	return 0
}

func validBucketCORSArgs(args []string) bool {
	if len(args) < 3 || !api.ValidAppSlug(args[1]) {
		return false
	}
	if id, err := uuid.Parse(args[2]); err != nil || id.String() != args[2] {
		return false
	}
	switch args[0] {
	case "get", "delete":
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
		defer f.Close() //nolint:errcheck
		r = f
	}
	return decodeBucketCORSFile(r)
}

// decodeBucketCORSFile reads `{"rules": [...]}`, the body the API takes, and
// checks it with the API's own rule bounds before any request.
func decodeBucketCORSFile(r io.Reader) ([]api.ObjectBucketCORSRule, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBucketCORSFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBucketCORSFileBytes {
		return nil, fmt.Errorf("CORS rules exceed %d bytes", maxBucketCORSFileBytes)
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	var in api.ObjectBucketCORSRequest
	if err := d.Decode(&in); err != nil {
		return nil, err
	}
	var extra any
	if in.Rules == nil || d.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf(`expected one {"rules": [...]} object`)
	}
	if !api.ValidObjectBucketCORSRules(in.Rules) {
		return nil, fmt.Errorf("rules need 1–%d entries, each with allowed_origins (* or http(s) origins with at most one *) and allowed_methods (GET, PUT, POST, DELETE, HEAD); use `gregale bucket cors delete` to remove every rule", api.MaxObjectBucketCORSRules)
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
	case "get":
		return c.GetObjectBucketCORS(ctx, args[1], args[2])
	case "set":
		return c.PutObjectBucketCORS(ctx, args[1], args[2], rules)
	case "delete":
		if err := c.DeleteObjectBucketCORS(ctx, args[1], args[2]); err != nil {
			return api.ObjectBucketCORS{}, err
		}
		return api.ObjectBucketCORS{BucketID: args[2], Rules: []api.ObjectBucketCORSRule{}}, nil
	}
	return api.ObjectBucketCORS{}, fmt.Errorf("unknown CORS action %q", args[0])
}

func printBucketCORS(w io.Writer, action string, out api.ObjectBucketCORS) {
	switch {
	case action == "delete":
		_, _ = fmt.Fprintf(w, "Removed CORS rules from bucket %s.\n", out.BucketID)
		return
	case len(out.Rules) == 0:
		_, _ = fmt.Fprintf(w, "Bucket %s has no CORS rules.\n", out.BucketID)
		return
	case action == "set":
		_, _ = fmt.Fprintf(w, "Saved %d CORS rule(s) for bucket %s (revision %d).\n", len(out.Rules), out.BucketID, out.Revision)
		return
	}
	_, _ = fmt.Fprintf(w, "CORS rules for bucket %s (revision %d):\n", out.BucketID, out.Revision)
	for i, rule := range out.Rules {
		parts := []string{
			"origins " + strings.Join(rule.AllowedOrigins, ", "),
			"methods " + strings.Join(rule.AllowedMethods, ", "),
		}
		if len(rule.AllowedHeaders) > 0 {
			parts = append(parts, "headers "+strings.Join(rule.AllowedHeaders, ", "))
		}
		if len(rule.ExposeHeaders) > 0 {
			parts = append(parts, "expose "+strings.Join(rule.ExposeHeaders, ", "))
		}
		if rule.MaxAgeSeconds > 0 {
			parts = append(parts, fmt.Sprintf("max age %ds", rule.MaxAgeSeconds))
		}
		label := fmt.Sprintf("%d", i+1)
		if rule.ID != "" {
			label += " (" + rule.ID + ")"
		}
		_, _ = fmt.Fprintf(w, "  %s  %s\n", label, strings.Join(parts, " · "))
	}
}
