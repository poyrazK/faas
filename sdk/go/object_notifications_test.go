package faas_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestPublicObjectNotificationClient(t *testing.T) {
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method)
		if r.Header.Get("Authorization") != "Bearer token" || r.URL.EscapedPath() != "/v1/apps/demo%2Fx/buckets/bucket%2Fx/notifications" {
			t.Error(r.Method, r.URL, r.Header.Get("Authorization"))
		}
		if r.Method == "PUT" {
			var in faas.ObjectBucketNotificationsRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Rules) != 1 || in.Rules[0].Prefix != "images/目录" || in.Rules[0].Events[0] != "s3:ObjectCreated:Put" {
				t.Error(in)
			}
		}
		_, _ = fmt.Fprint(w, `{"bucket_id":"bucket","revision":2,"rules":[]}`)
	}))
	defer server.Close()
	c, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if p, e := c.PutObjectBucketNotifications(ctx, "demo/x", "bucket/x", faas.ObjectBucketNotificationsRequest{Rules: []faas.ObjectNotificationRule{{ID: "images", Destination: "arn:gregale:sqs:us-east-1:11111111-1111-4111-8111-111111111111:22222222-2222-4222-8222-222222222222/storage", Events: []string{"s3:ObjectCreated:Put"}, Prefix: "images/目录"}}}); e != nil || p.Revision != 2 {
		t.Fatal(p, e)
	}
	if _, err = c.GetObjectBucketNotifications(ctx, "demo/x", "bucket/x"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.DeleteObjectBucketNotifications(ctx, "demo/x", "bucket/x"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || calls[0] != "PUT" || calls[1] != "GET" || calls[2] != "DELETE" {
		t.Fatal(calls)
	}
}

func TestPublicObjectComputeTriggerClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != "PUT" || r.Header.Get("Authorization") != "Bearer token" || r.URL.EscapedPath() != "/v1/apps/demo%2Fx/buckets/bucket%2Fx/compute-triggers" {
			t.Error(r.Method, r.URL)
		}
		var in faas.ReconcileObjectStorageComputeTriggersRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ExpectedRevision == nil || *in.ExpectedRevision != 3 || in.Triggers == nil || len(in.Triggers) != 0 {
			t.Error(in, err)
		}
		_, _ = fmt.Fprint(w, `{"bucket_id":"bucket","revision":4,"rules":[],"managed_rule_ids":["managed"]}`)
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	revision := int64(3)
	p, err := client.ReconcileObjectStorageComputeTriggers(ctx, "demo/x", "bucket/x", faas.ReconcileObjectStorageComputeTriggersRequest{ExpectedRevision: &revision, Triggers: []faas.ObjectStorageComputeTrigger{}})
	if err != nil || !called || p.Revision != 4 || len(p.ManagedRuleIDs) != 1 {
		t.Fatal(p, err)
	}
}
