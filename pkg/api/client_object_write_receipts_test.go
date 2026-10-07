package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientObjectWriteReceipts(t *testing.T) {
	for _, list := range []bool{false, true} {
		t.Run(map[bool]string{true: "list", false: "get"}[list], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := "/v1/apps/demo/buckets/bucket/write-receipts"
				if !list {
					path += "/receipt"
				}
				if r.Method != "GET" || r.URL.Path != path || r.Header.Get("Authorization") != "Bearer token" {
					t.Error(r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				if list {
					if r.URL.Query().Get("status") != "pending" || r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("cursor") != "next+cursor" {
						t.Error(r.URL.Query())
					}
					_ = json.NewEncoder(w).Encode(ObjectWriteReceiptList{Items: []ObjectWriteReceipt{{ID: "receipt", Status: "pending"}}, NextCursor: "another"})
				} else {
					_ = json.NewEncoder(w).Encode(ObjectWriteReceipt{ID: "receipt", Status: "completed"})
				}
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "token")
			if list {
				page, err := c.ListObjectWriteReceipts(context.Background(), "demo", "bucket", "pending", 2, "next+cursor")
				if err != nil || len(page.Items) != 1 || page.NextCursor != "another" {
					t.Fatal(page, err)
				}
			} else {
				receipt, err := c.GetObjectWriteReceipt(context.Background(), "demo", "bucket", "receipt")
				if err != nil || receipt.Status != "completed" {
					t.Fatal(receipt, err)
				}
			}
		})
	}
}
