package api

// adr: 202610171907

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrgObjectClientPreservesTenantAndBucketPaths(t *testing.T) {
	var request string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request = r.Method + " " + r.URL.EscapedPath()
		if r.Header.Get("Authorization") != "Bearer company-token" {
			t.Error("missing organization credential")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	c := NewClient(server.URL, "company-token")
	ctx := context.Background()
	for _, tc := range []struct {
		want string
		call func() error
	}{
		{"GET /v1/orgs/my%20company/apps/app%2Fid/buckets", func() error {
			_, err := c.ListOrgObjectBuckets(ctx, "my company", "app/id")
			return err
		}},
		{"POST /v1/orgs/my%20company/apps/app%2Fid/buckets/bucket%2Fid/compute-bindings", func() error {
			_, err := c.CreateOrgObjectStorageComputeBinding(ctx, "my company", "app/id", "bucket/id", CreateObjectStorageComputeBindingRequest{Permission: "read_write"})
			return err
		}},
		{"POST /v1/orgs/my%20company/apps/app%2Fid/buckets/bucket%2Fid/compute-bindings/binding%2Fid/rotate", func() error {
			_, err := c.RotateOrgObjectStorageComputeBinding(ctx, "my company", "app/id", "bucket/id", "binding/id")
			return err
		}},
		{"PUT /v1/orgs/my%20company/apps/app%2Fid/buckets/bucket%2Fid/access-grants/key%2Fid", func() error {
			_, err := c.SetOrgObjectBucketAccessGrant(ctx, "my company", "app/id", "bucket/id", "key/id", SetObjectBucketAccessGrantRequest{Permission: "read"})
			return err
		}},
		{"GET /v1/orgs/my%20company/object-storage-usage", func() error {
			_, err := NewPreviewClient(c).GetOrgObjectStorageUsage(ctx, "my company")
			return err
		}},
	} {
		if err := tc.call(); err != nil {
			t.Fatal(err)
		}
		if request != tc.want {
			t.Fatalf("request %s; want %s", request, tc.want)
		}
	}
}
