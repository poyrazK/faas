package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type capacityCLIClient struct{ action, app, bucket, id string }

func (c *capacityCLIClient) CreateObjectCapacityReconciliation(_ context.Context, app, bucket string) (api.ObjectCapacityReconciliation, error) {
	c.action, c.app, c.bucket = "start", app, bucket
	return api.ObjectCapacityReconciliation{ID: "created"}, nil
}
func (c *capacityCLIClient) GetObjectCapacityReconciliation(_ context.Context, app, bucket, id string) (api.ObjectCapacityReconciliation, error) {
	c.action, c.app, c.bucket, c.id = "status", app, bucket, id
	return api.ObjectCapacityReconciliation{ID: id}, nil
}
func (c *capacityCLIClient) CancelObjectCapacityReconciliation(_ context.Context, app, bucket, id string) (api.ObjectCapacityReconciliation, error) {
	c.action, c.app, c.bucket, c.id = "cancel", app, bucket, id
	return api.ObjectCapacityReconciliation{ID: id}, nil
}
func TestBucketCapacityCLI(t *testing.T) {
	for _, action := range []string{"start", "status", "cancel"} {
		t.Run(action, func(t *testing.T) {
			c := &capacityCLIClient{}
			j, err := runBucketCapacity(context.Background(), c, action, "demo", "bucket", []string{"job"})
			if err != nil || j.ID == "" || c.action != action || c.app != "demo" || c.bucket != "bucket" {
				t.Fatal(c, j, err)
			}
		})
	}
}
