package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBucketLifecycleCLI(t *testing.T) {
	const bucket = "00000000-0000-0000-0000-000000000001"
	in, err := decodeBucketLifecycleFile(strings.NewReader(`{"rules":[{"status":"Enabled","filter":{"prefix":"tmp/"},"abort_incomplete_multipart_days":1}]}`))
	if err != nil || in.Rules[0].ID == "" {
		t.Fatal(in, err)
	}
	for _, body := range []string{`null`, `{"rules":[]}`, `{"rules":null}`, `{"rules":[{"status":"Enabled","transition":{}}]}`, `{"rules":[{"status":"Enabled","expiration":{"days":0}}]}`, `{} {}`, strings.Repeat(" ", int(api.MaxObjectLifecycleBodyBytes)+1)} {
		if _, err = decodeBucketLifecycleFile(strings.NewReader(body)); err == nil {
			t.Fatal("accepted invalid lifecycle", body[:min(len(body), 80)])
		}
	}
	for _, args := range [][]string{{}, {"get", "bad/app", bucket}, {"set", "demo", bucket}, {"status", "demo", bucket, "not-uuid"}, {"get", "demo", bucket, "extra"}, {"unknown", "demo", bucket}} {
		if validBucketLifecycleArgs(args) {
			t.Fatal("accepted invalid arguments", args)
		}
	}
	for _, action := range []string{"get", "set", "clear", "scan", "status"} {
		t.Run(action, func(t *testing.T) {
			method := map[string]string{"get": "GET", "set": "PUT", "clear": "DELETE", "scan": "POST", "status": "GET"}[action]
			path := "/v1/apps/demo/buckets/" + bucket + "/lifecycle"
			args := []string{action, "demo", bucket}
			if action == "set" {
				args = append(args, "-")
			}
			if action == "scan" {
				path += "/scans"
			}
			if action == "status" {
				path += "/scans/" + bucket
				args = append(args, bucket)
			}
			if !validBucketLifecycleArgs(args) {
				t.Fatal(args)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != method || r.URL.Path != path {
					t.Error(r.Method, r.URL.Path)
				}
				if action == "scan" {
					w.WriteHeader(202)
				}
				_, _ = fmt.Fprint(w, `{"bucket_id":"`+bucket+`","revision":2,"rules":[],"id":"`+bucket+`","state":"scanning","phase":"multipart"}`)
			}))
			defer srv.Close()
			if _, err := runBucketLifecycle(t.Context(), api.NewClient(srv.URL, "token"), args, in); err != nil {
				t.Fatal(err)
			}
		})
	}
}
