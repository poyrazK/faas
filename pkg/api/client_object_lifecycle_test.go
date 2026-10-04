package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientObjectLifecycle(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.EscapedPath())
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing authentication")
		}
		if r.Method == "PUT" {
			var in ObjectBucketLifecycleRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Rules) != 1 || in.Rules[0].AbortIncompleteMultipartDays == nil {
				t.Error(in)
			}
		}
		if r.Method == "POST" {
			w.WriteHeader(http.StatusAccepted)
		}
		if r.Method == "POST" || r.URL.EscapedPath() == "/v1/apps/demo%2Fx/buckets/bucket%2Fx/lifecycle/scans/scan%2Fx" {
			_, _ = fmt.Fprint(w, `{"id":"scan","bucket_id":"bucket","revision":2,"state":"scanning","phase":"multipart","scanned_keys":0,"scanned_uploads":3}`)
		} else {
			_, _ = fmt.Fprint(w, `{"bucket_id":"bucket","revision":2,"rules":[]}`)
		}
	}))
	defer srv.Close()
	c, ctx := NewClient(srv.URL, "token"), t.Context()
	days := int32(1)
	if p, err := c.PutObjectBucketLifecycle(ctx, "demo/x", "bucket/x", ObjectBucketLifecycleRequest{Rules: []ObjectLifecycleRule{{Status: "Enabled", AbortIncompleteMultipartDays: &days}}}); err != nil || p.Revision != 2 {
		t.Fatal(p, err)
	}
	if p, err := c.GetObjectBucketLifecycle(ctx, "demo/x", "bucket/x"); err != nil || p.Revision != 2 {
		t.Fatal(p, err)
	}
	if p, err := c.DeleteObjectBucketLifecycle(ctx, "demo/x", "bucket/x"); err != nil || p.Revision != 2 {
		t.Fatal(p, err)
	}
	if j, err := c.CreateObjectLifecycleScan(ctx, "demo/x", "bucket/x"); err != nil || j.ScannedUploads != 3 {
		t.Fatal(j, err)
	}
	if j, err := c.GetObjectLifecycleScan(ctx, "demo/x", "bucket/x", "scan/x"); err != nil || j.Phase != "multipart" {
		t.Fatal(j, err)
	}
	want := []string{"PUT /v1/apps/demo%2Fx/buckets/bucket%2Fx/lifecycle", "GET /v1/apps/demo%2Fx/buckets/bucket%2Fx/lifecycle", "DELETE /v1/apps/demo%2Fx/buckets/bucket%2Fx/lifecycle", "POST /v1/apps/demo%2Fx/buckets/bucket%2Fx/lifecycle/scans", "GET /v1/apps/demo%2Fx/buckets/bucket%2Fx/lifecycle/scans/scan%2Fx"}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatal(calls)
		}
	}
}
