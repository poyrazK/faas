package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestObjectDeletionClient(t *testing.T) {
	selector := "null"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == http.MethodPost {
			var in ObjectDeletionRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.ID != "id" || in.Key != "目录 /+%.txt" || in.VersionID != selector {
				t.Error(in)
			}
			w.WriteHeader(202)
		} else if r.URL.Path != "/v1/apps/demo/buckets/bucket/objects/deletions/id" {
			t.Error(r.URL)
		}
		_, _ = w.Write([]byte(`{"id":"id","bucket_id":"bucket","key":"目录 /+%.txt","selector":"null","state":"dispatched","delete_marker":false,"created_at":"2026-10-02T12:00:00Z","updated_at":"2026-10-02T12:00:00Z"}`))
	}))
	defer server.Close()
	c := NewClient(server.URL, "token")
	j, e := c.CreateObjectDeletion(t.Context(), "demo", "bucket", ObjectDeletionRequest{ID: "id", Key: "目录 /+%.txt", VersionID: "null"})
	if e != nil || j.State != "dispatched" {
		t.Fatal(j, e)
	}
	j, e = c.GetObjectDeletion(t.Context(), "demo", "bucket", "id")
	if e != nil || j.Selector != "null" || calls != 2 {
		t.Fatal(j, e, calls)
	}
	selector = "12345678-1234-4234-8234-123456789abc"
	if _, e = c.CreateObjectDeletion(t.Context(), "demo", "bucket", ObjectDeletionRequest{ID: "id", Key: "目录 /+%.txt", VersionID: selector}); e != nil || calls != 3 {
		t.Fatal(e, calls)
	}
}
