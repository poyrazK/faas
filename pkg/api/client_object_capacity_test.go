package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientObjectCapacityReconciliation(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				want := "/v1/apps/demo/buckets/bucket/capacity-reconciliations"
				if method != http.MethodPost {
					want += "/job"
				}
				if r.Method != method || r.URL.Path != want || r.Header.Get("Authorization") != "Bearer token" {
					t.Error(r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"id":"job","state":"waiting","inventory_scope":"all_versions","scanned_pages":2,"scanned_bytes":30,"scanned_versions":3}`)
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "token")
			var j ObjectCapacityReconciliation
			var err error
			switch method {
			case http.MethodPost:
				j, err = c.CreateObjectCapacityReconciliation(context.Background(), "demo", "bucket")
			case http.MethodGet:
				j, err = c.GetObjectCapacityReconciliation(context.Background(), "demo", "bucket", "job")
			default:
				j, err = c.CancelObjectCapacityReconciliation(context.Background(), "demo", "bucket", "job")
			}
			if err != nil || j.ID != "job" || j.InventoryScope != "all_versions" || j.ScannedPages != 2 || j.ScannedBytes != 30 || j.ScannedVersions != 3 {
				t.Fatal(j, err)
			}
		})
	}
}
