package logarchive

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// TestS3_ListObjectKeysPagesBothMarkerStyles pins paging for GCS (which sends
// NextMarker) and S3 (which leaves the caller to resume after the last key).
func TestS3_ListObjectKeysPagesBothMarkerStyles(t *testing.T) {
	keys := []string{"faas-logs/i-1/2026/10/01.jsonl.gz", "faas-logs/i-1/2026/10/02.jsonl.gz", "faas-logs/i-1/2026/10/03.jsonl.gz"}
	for _, sendNextMarker := range []bool{true, false} {
		t.Run(fmt.Sprintf("next_marker=%v", sendNextMarker), func(t *testing.T) {
			var queries []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/mybucket" {
					t.Errorf("request = %s %s, want GET /mybucket", r.Method, r.URL.Path)
				}
				if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
					t.Errorf("list request is not SigV4-signed")
				}
				q := r.URL.Query()
				queries = append(queries, q.Get("prefix")+"|"+q.Get("marker"))
				var page []string
				for _, key := range keys {
					if strings.HasPrefix(key, q.Get("prefix")) && key > q.Get("marker") {
						page = append(page, key)
					}
				}
				truncated := len(page) > 2
				if truncated {
					page = page[:2]
				}
				var b strings.Builder
				b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://doc.s3.amazonaws.com/2006-03-01">`)
				fmt.Fprintf(&b, "<IsTruncated>%v</IsTruncated>", truncated)
				if truncated && sendNextMarker {
					fmt.Fprintf(&b, "<NextMarker>%s</NextMarker>", page[len(page)-1])
				}
				for _, key := range page {
					fmt.Fprintf(&b, "<Contents><Key>%s</Key><Size>10</Size></Contents>", key)
				}
				b.WriteString("</ListBucketResult>")
				_, _ = w.Write([]byte(b.String()))
			}))
			defer srv.Close()
			c, err := NewS3Client(srv.URL, "us-east-1", "mybucket", "AKIA", "secret")
			if err != nil {
				t.Fatalf("NewS3Client: %v", err)
			}
			var got []string
			marker := ""
			for {
				page, next, err := c.ListObjectKeys(context.Background(), "faas-logs/i-1/", marker, 1000)
				if err != nil {
					t.Fatalf("ListObjectKeys: %v", err)
				}
				got = append(got, page...)
				if next == "" {
					break
				}
				marker = next
			}
			if !slices.Equal(got, keys) {
				t.Fatalf("keys = %v, want %v", got, keys)
			}
			want := []string{"faas-logs/i-1/|", "faas-logs/i-1/|" + keys[1]}
			if !slices.Equal(queries, want) {
				t.Fatalf("queries = %v, want %v", queries, want)
			}
		})
	}
}

func TestS3_ListObjectKeysRejectsATruncatedPageWithoutAMarker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<ListBucketResult><IsTruncated>true</IsTruncated></ListBucketResult>`))
	}))
	defer srv.Close()
	c, _ := NewS3Client(srv.URL, "us-east-1", "mybucket", "AKIA", "secret")
	if _, _, err := c.ListObjectKeys(context.Background(), "faas-logs/i-1/", "", 1000); err == nil {
		t.Fatal("a truncated page with no keys and no marker would loop forever; want an error")
	}
}

func TestS3_DeleteObjectTreatsMissingAsDeleted(t *testing.T) {
	for _, tc := range []struct {
		status  int
		wantErr bool
	}{
		{http.StatusNoContent, false},
		{http.StatusNotFound, false},
		{http.StatusForbidden, true},
		{http.StatusInternalServerError, true},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || r.URL.Path != "/mybucket/faas-logs/i-1/2026/10/01.jsonl.gz" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			c, _ := NewS3Client(srv.URL, "us-east-1", "mybucket", "AKIA", "secret")
			err := c.DeleteObject(context.Background(), "faas-logs/i-1/2026/10/01.jsonl.gz")
			if (err != nil) != tc.wantErr {
				t.Fatalf("DeleteObject err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
