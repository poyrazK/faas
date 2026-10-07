package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// adr: 638
func TestObjectVersionsClient(t *testing.T) {
	const key, version = "目录 /+%.txt", "12345678-1234-4234-8234-123456789abc"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/v1/apps/demo/buckets/bucket/objects/versions" || q.Get("prefix") != "目录" || q.Get("delimiter") != "/" || q.Get("key_marker") != key || q.Get("version_id_marker") != version || q.Get("limit") != "1" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error("query identity changed", r.URL)
		}
		_ = json.NewEncoder(w).Encode(ObjectVersionList{Items: []ObjectVersion{{Key: key, VersionID: version}}, CommonPrefixes: []string{}, NextKeyMarker: key, NextVersionIDMarker: version})
	}))
	defer srv.Close()
	out, err := NewClient(srv.URL, "token").ListObjectBucketVersions(t.Context(), "demo", "bucket", ObjectVersionListRequest{Prefix: "目录", Delimiter: "/", KeyMarker: key, VersionIDMarker: version, Limit: 1})
	if err != nil || len(out.Items) != 1 || out.Items[0].Key != key || out.Items[0].VersionID != version || out.NextKeyMarker != key || out.NextVersionIDMarker != version || out.CommonPrefixes == nil {
		t.Fatal(out, err)
	}
}
