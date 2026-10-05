package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeleteObjectBucketVersionClient(t *testing.T) {
	key, id := "目录 /+%.txt", "ad7b43a1-4a3f-4f6d-9518-d1f0e4c79b78"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || r.URL.Path != "/v1/apps/demo/buckets/bucket/objects/versions" || r.URL.Query().Get("key") != key || r.URL.Query().Get("version_id") != id {
			t.Error("lost exact public selector", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version_id":"` + id + `","delete_marker":true}`))
	}))
	t.Cleanup(srv.Close)
	out, err := NewClient(srv.URL, "token").DeleteObjectBucketVersion(t.Context(), "demo", "bucket", key, id)
	if err != nil || out.VersionID != id || !out.DeleteMarker {
		t.Fatal(out, err)
	}
}
