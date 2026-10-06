package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestObjectTaggingClient(t *testing.T) {
	key, version := "目录 /+%.txt", "12345678-1234-4234-8234-123456789abc"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/apps/demo/buckets/bucket/objects/tags" || r.URL.Query().Get("key") != key || r.URL.Query().Get("version_id") != version || r.Header.Get("Authorization") != "Bearer token" {
			t.Error(r.URL)
		}
		out := ObjectTaggingResult{VersionID: version, Tags: map[string]string{}}
		if r.Method == "PUT" {
			var input ObjectTaggingRequest
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.Tags["team"] != "core" {
				t.Error(input)
			}
			out.Tags = input.Tags
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "token")
	if _, err := c.GetObjectBucketTags(t.Context(), "demo", "bucket", key, version); err != nil {
		t.Fatal(err)
	}
	if out, err := c.PutObjectBucketTags(t.Context(), "demo", "bucket", key, version, ObjectTaggingRequest{Tags: map[string]string{"team": "core"}}); err != nil || out.VersionID != version || out.Tags["team"] != "core" {
		t.Fatal(out, err)
	}
	if out, err := c.DeleteObjectBucketTags(t.Context(), "demo", "bucket", key, version); err != nil || out.Tags == nil || len(out.Tags) != 0 || calls != 3 {
		t.Fatal(out, err, calls)
	}
}
