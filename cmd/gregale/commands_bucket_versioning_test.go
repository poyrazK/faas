package main

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
)

type versioningCLIClient struct{ status, app, bucket string }

func (c *versioningCLIClient) GetObjectBucketVersioning(_ context.Context, app, bucket string) (api.ObjectBucketVersioning, error) {
	c.app, c.bucket = app, bucket
	return api.ObjectBucketVersioning{State: "ready"}, nil
}
func (c *versioningCLIClient) PutObjectBucketVersioning(_ context.Context, app, bucket, status string) (api.ObjectBucketVersioning, error) {
	c.app, c.bucket, c.status = app, bucket, status
	return api.ObjectBucketVersioning{DesiredStatus: status, State: "waiting"}, nil
}
func TestBucketVersioningCLI(t *testing.T) {
	for _, action := range []string{"status", "enable", "suspend"} {
		t.Run(action, func(t *testing.T) {
			c := &versioningCLIClient{}
			j, err := runBucketVersioning(t.Context(), c, action, "demo", "bucket")
			if err != nil || c.app != "demo" || c.bucket != "bucket" || action == "enable" && j.DesiredStatus != "Enabled" || action == "suspend" && j.DesiredStatus != "Suspended" {
				t.Fatal(c, j, err)
			}
		})
	}
}
