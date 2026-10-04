package faas_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

// adr: 562
func TestCopySourceClient(t *testing.T) {
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method)
		base := "/v1/apps/demo%2Fx/buckets/bucket%2Fx/s3-credentials/credential%2Fx/copy-sources"
		expected := base
		if r.Method != "GET" {
			expected += "/source%2Fx"
		}
		if r.URL.EscapedPath() != expected || r.Header.Get("Authorization") != "Bearer token" {
			t.Error(r.URL, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		if r.Method == "PUT" {
			var in faas.SetObjectS3CopySourceRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.Prefix != "allowed/" {
				t.Error(in)
			}
		}
		item := `{"source_bucket_id":"source/x","prefix":"allowed/","created_at":"2026-10-04T00:00:00Z","updated_at":"2026-10-04T00:00:00Z"}`
		if r.Method == "GET" {
			_, _ = fmt.Fprint(w, `{"items":[`+item+`]}`)
		} else {
			_, _ = fmt.Fprint(w, item)
		}
	}))
	defer server.Close()
	c, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	grant, err := c.SetObjectS3CopySource(t.Context(), "demo/x", "bucket/x", "credential/x", "source/x", faas.SetObjectS3CopySourceRequest{Prefix: "allowed/"})
	if err != nil || grant.SourceBucketID != "source/x" {
		t.Fatal(grant, err)
	}
	list, err := c.ListObjectS3CopySources(t.Context(), "demo/x", "bucket/x", "credential/x")
	if err != nil || len(list.Items) != 1 || list.Items[0] != grant {
		t.Fatal(list, err)
	}
	if err = c.DeleteObjectS3CopySource(t.Context(), "demo/x", "bucket/x", "credential/x", "source/x"); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(calls) != "[PUT GET DELETE]" {
		t.Fatal(calls)
	}
}
