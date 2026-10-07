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
