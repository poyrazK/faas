package faas_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestBucketCatalogTransferDiscovery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v1/apps/demo%20app/buckets" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("unexpected catalog request %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[],"enabled":true,"regions":["us-east-1"],"default_region":"us-east-1","max_upload_bytes":5497558138880,"max_buckets_per_app":10,"max_single_put_bytes":536870912,"max_part_bytes":536870912,"transfer_timeout_seconds":7200,"upload_profile":"direct"}`)
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.ListObjectBuckets(context.Background(), "demo app")
	if err != nil || got.MaxUploadBytes != 5<<40 || got.MaxSinglePutBytes != 512<<20 || got.MaxPartBytes != 512<<20 || got.TransferTimeoutSeconds != 7200 || got.UploadProfile != "direct" {
		t.Fatalf("transfer discovery: %+v %v", got, err)
	}
}
