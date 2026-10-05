package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type defaultEncryptionCLIClient struct {
	method, app, bucket string
	encryption          api.ObjectEncryption
}

func (c *defaultEncryptionCLIClient) GetObjectBucketEncryption(_ context.Context, app, bucket string) (api.ObjectBucketEncryption, error) {
	c.method, c.app, c.bucket = "GET", app, bucket
	return api.ObjectBucketEncryption{State: "ready"}, nil
}
func (c *defaultEncryptionCLIClient) PutObjectBucketEncryption(_ context.Context, app, bucket string, encryption api.ObjectEncryption) (api.ObjectBucketEncryption, error) {
	c.method, c.app, c.bucket, c.encryption = "PUT", app, bucket, encryption
	return api.ObjectBucketEncryption{State: "waiting", DesiredEncryption: &encryption}, nil
}
func (c *defaultEncryptionCLIClient) DeleteObjectBucketEncryption(_ context.Context, app, bucket string) (api.ObjectBucketEncryption, error) {
	c.method, c.app, c.bucket = "DELETE", app, bucket
	return api.ObjectBucketEncryption{State: "waiting"}, nil
}

// adr: 561
func TestBucketDefaultEncryptionCLI(t *testing.T) {
	key := "arn:gregale:kms:aws:11111111-1111-4111-8111-111111111111:key/22222222-2222-4222-8222-222222222222"
	for _, tc := range []struct {
		args   []string
		method string
	}{
		{[]string{"status", "demo", "bucket"}, "GET"},
		{[]string{"clear", "demo", "bucket"}, "DELETE"},
		{[]string{"AES256", "demo", "bucket"}, "PUT"},
		{[]string{"aws:kms", "demo", "bucket", key, "true"}, "PUT"},
		{[]string{"aws:kms", "demo", "bucket", key, "false"}, "PUT"},
		{[]string{"aws:kms:dsse", "demo", "bucket", key}, "PUT"},
	} {
		t.Run(tc.args[0]+tc.args[len(tc.args)-1], func(t *testing.T) {
			c := &defaultEncryptionCLIClient{}
			_, err := runBucketDefaultEncryption(t.Context(), c, tc.args)
			if err != nil || c.method != tc.method || c.app != "demo" || c.bucket != "bucket" {
				t.Fatal(c, err)
			}
			if tc.method == "PUT" && (c.encryption.Algorithm != tc.args[0] || !c.encryption.Valid()) {
				t.Fatal(c.encryption)
			}
			if len(tc.args) == 5 && (c.encryption.BucketKeyEnabled == nil || *c.encryption.BucketKeyEnabled != (tc.args[4] == "true")) {
				t.Fatal("bucket key option lost", c.encryption)
			}
		})
	}
	for _, args := range [][]string{
		nil, {"unknown", "demo", "bucket"}, {"status", "demo", "bucket", "extra"},
		{"AES256", "demo", "bucket", key}, {"aws:kms", "demo", "bucket"},
		{"aws:kms", "demo", "bucket", "native-key"}, {"aws:kms", "demo", "bucket", key, "yes"},
		{"aws:kms:dsse", "demo", "bucket", key, "true"},
	} {
		c := &defaultEncryptionCLIClient{}
		if _, err := runBucketDefaultEncryption(t.Context(), c, args); err == nil || c.method != "" {
			t.Fatal("invalid request dispatched", args, c, err)
		}
	}
}
