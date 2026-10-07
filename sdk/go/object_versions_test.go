package faas_test

import (
	"encoding/json"
	faas "github.com/poyrazK/faas/sdk/go"
	"net/http"
	"net/http/httptest"
	"testing"
)

// adr: 678
func TestObjectVersionsClient(t *testing.T) {
	const key, version = "目录 /+%.txt", "12345678-1234-4234-8234-123456789abc"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/v1/apps/demo/buckets/bucket/objects/versions" || q.Get("prefix") != "目录" || q.Get("delimiter") != "/" || q.Get("key_marker") != key || q.Get("version_id_marker") != version || q.Get("limit") != "1" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error("query identity changed", r.URL)
		}
		_ = json.NewEncoder(w).Encode(faas.ObjectVersionList{Items: []faas.ObjectVersion{{Key: key, VersionID: version}}, CommonPrefixes: []string{}, NextKeyMarker: key, NextVersionIDMarker: version})
	}))
	defer srv.Close()
	client, err := faas.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	out, err := client.ListObjectBucketVersions(t.Context(), "demo", "bucket", faas.ObjectVersionListRequest{Prefix: "目录", Delimiter: "/", KeyMarker: key, VersionIDMarker: version, Limit: 1})
	if err != nil || len(out.Items) != 1 || out.Items[0].Key != key || out.Items[0].VersionID != version || out.NextKeyMarker != key || out.NextVersionIDMarker != version || out.CommonPrefixes == nil {
		t.Fatal(out, err)
	}
}
