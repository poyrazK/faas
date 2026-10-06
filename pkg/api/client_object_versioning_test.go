package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientObjectBucketVersioning(t *testing.T) {
	for _, method := range []string{"GET", "PUT"} {
		t.Run(method, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != method || r.URL.Path != "/v1/apps/demo/buckets/bucket/versioning" || r.Header.Get("Authorization") != "Bearer token" {
					t.Error(r.Method, r.URL.Path)
				}
				if method == "PUT" {
					var in ObjectBucketVersioningRequest
					if json.NewDecoder(r.Body).Decode(&in) != nil || in.Status != "Enabled" {
						t.Error(in)
					}
				}
				_, _ = fmt.Fprint(w, `{"bucket_id":"bucket","desired_status":"Enabled","observed_status":"Enabled","state":"propagating","versions_required":true,"revision":1}`)
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "token")
			var j ObjectBucketVersioning
			var err error
			if method == "GET" {
				j, err = c.GetObjectBucketVersioning(t.Context(), "demo", "bucket")
			} else {
				j, err = c.PutObjectBucketVersioning(t.Context(), "demo", "bucket", "Enabled")
			}
			if err != nil || j.State != "propagating" || !j.VersionsRequired || j.Revision != 1 {
				t.Fatal(j, err)
			}
		})
	}
}
