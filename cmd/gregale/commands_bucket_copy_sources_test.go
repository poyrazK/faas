package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type copySourcesCLIClient struct {
	args   []string
	prefix string
	method string
}

func (c *copySourcesCLIClient) ListObjectS3CopySources(_ context.Context, app, bucket, credential string) (api.ObjectS3CopySourceList, error) {
	c.args = []string{app, bucket, credential}
	c.method = "GET"
	return api.ObjectS3CopySourceList{Items: []api.ObjectS3CopySource{}}, nil
}
func (c *copySourcesCLIClient) SetObjectS3CopySource(_ context.Context, app, bucket, credential, source string, r api.SetObjectS3CopySourceRequest) (api.ObjectS3CopySource, error) {
	c.args = []string{app, bucket, credential, source}
	c.prefix = r.Prefix
	c.method = "PUT"
	return api.ObjectS3CopySource{SourceBucketID: source, Prefix: r.Prefix}, nil
}
func (c *copySourcesCLIClient) DeleteObjectS3CopySource(_ context.Context, app, bucket, credential, source string) error {
	c.args = []string{app, bucket, credential, source}
	c.method = "DELETE"
	return nil
}

// adr: 562
func TestBucketCopySourcesCLI(t *testing.T) {
	bucket, credential, source := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, tc := range []struct{ op, method, prefix string }{{"list", "GET", ""}, {"grant", "PUT", "allowed/"}, {"revoke", "DELETE", ""}} {
		c := &copySourcesCLIClient{}
		args := []string{tc.op, "demo", bucket, credential}
		want := args[1:]
		if tc.op != "list" {
			args = append(args, source)
			want = append([]string{}, args[1:]...)
		}
		if tc.prefix != "" {
			args = append(args, tc.prefix)
		}
		if _, err := runBucketCopySources(t.Context(), c, args); err != nil || c.method != tc.method || !reflect.DeepEqual(c.args, want) || c.prefix != tc.prefix {
			t.Fatal(c, err)
		}
	}
	for _, args := range [][]string{nil, {"unknown", "demo", bucket, credential}, {"list", "demo", bucket, credential, source}, {"grant", "demo", bucket, credential}, {"revoke", "bad/slug", bucket, credential, source}, {"grant", "demo", "bad", credential, source}} {
		c := &copySourcesCLIClient{}
		if _, err := runBucketCopySources(t.Context(), c, args); err == nil || c.method != "" {
			t.Fatal("invalid dispatch", args, c, err)
		}
	}
}
