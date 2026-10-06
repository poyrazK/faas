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

func TestPublicLifecycleClient(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.EscapedPath())
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing authentication")
		}
		if r.Method == http.MethodPut {
			var in faas.ObjectBucketLifecycleRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Rules) != 1 || in.Rules[0].Expiration == nil || in.Rules[0].Expiration.ExpiredObjectDeleteMarker == nil || *in.Rules[0].Expiration.ExpiredObjectDeleteMarker {
				t.Error(in)
			}
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusAccepted)
		}
		_, _ = fmt.Fprint(w, `{"id":"scan","bucket_id":"bucket","revision":2,"rules":[],"phase":"multipart","state":"completed","scanned_uploads":3}`)
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	marker := false
	ctx := context.Background()
	in := faas.ObjectBucketLifecycleRequest{Rules: []faas.ObjectLifecycleRule{{Status: "Enabled", Filter: faas.ObjectLifecycleFilter{}, Expiration: &faas.ObjectLifecycleExpiration{ExpiredObjectDeleteMarker: &marker}}}}
	if p, e := c.PutObjectBucketLifecycle(ctx, "demo/x", "bucket/x", in); e != nil || p.Revision != 2 {
		t.Fatal(p, e)
	}
	if _, err = c.GetObjectBucketLifecycle(ctx, "demo/x", "bucket/x"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.DeleteObjectBucketLifecycle(ctx, "demo/x", "bucket/x"); err != nil {
		t.Fatal(err)
	}
	if j, e := c.CreateObjectLifecycleScan(ctx, "demo/x", "bucket/x"); e != nil || j.ScannedUploads != 3 {
		t.Fatal(j, e)
	}
	if j, e := c.GetObjectLifecycleScan(ctx, "demo/x", "bucket/x", "scan/x"); e != nil || j.Phase != "multipart" {
		t.Fatal(j, e)
	}
	want := []string{"PUT /v1/apps/demo%2Fx/buckets/bucket%2Fx/lifecycle", "GET /v1/apps/demo%2Fx/buckets/bucket%2Fx/lifecycle", "DELETE /v1/apps/demo%2Fx/buckets/bucket%2Fx/lifecycle", "POST /v1/apps/demo%2Fx/buckets/bucket%2Fx/lifecycle/scans", "GET /v1/apps/demo%2Fx/buckets/bucket%2Fx/lifecycle/scans/scan%2Fx"}
	if len(calls) != len(want) {
		t.Fatal(calls)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatal(calls)
		}
	}
}
