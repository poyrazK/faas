package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 955
func TestBucketConditionalUploadDispatch(t *testing.T) {
	for _, c := range []api.ObjectWriteConditions{{IfMatch: `"old"`}, {IfNoneMatch: "*"}} {
		t.Run(c.IfMatch+c.IfNoneMatch, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("ETag", `"new"`) }))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "input")
			if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
				t.Fatal(err)
			}
			fixture := &transferFixture{url: server.URL}
			out, err := runBucketTransfer(context.Background(), fixture, bucketTransferOptions{action: "upload", app: "demo", bucket: uuid.NewString(), key: "key", path: path, conditions: c})
			if err != nil || out.Status != "completed" || fixture.request.IfMatch != c.IfMatch || fixture.request.IfNoneMatch != c.IfNoneMatch {
				t.Fatal("CLI dropped write condition", err, out)
			}
			if err := os.WriteFile(path, []byte("large"), 0600); err != nil {
				t.Fatal(err)
			}
			fixture.calls = 0
			if _, err := runBucketTransfer(t.Context(), fixture, bucketTransferOptions{action: "upload", app: "demo", bucket: uuid.NewString(), key: "key", path: path, conditions: c}); err == nil || fixture.calls != 0 || fixture.multipart {
				t.Fatal("conditional write silently became unconditional multipart", err, fixture.calls)
			}
		})
	}
}

// adr: 955
func TestBucketConditionalUploadFlags(t *testing.T) {
	base := []string{"upload", "demo", uuid.NewString(), "key", "input"}
	for _, tc := range []struct {
		flags []string
		valid bool
	}{
		{[]string{"--if-match", `"old"`}, true}, {[]string{"--if-none-match", "*"}, true},
		{[]string{"--if-match", `"old"`, "--if-none-match", "*"}, false},
		{[]string{"--if-none-match", `"etag"`}, false},
		{[]string{"--if-match", ""}, false}, {[]string{"--if-none-match", ""}, false},
		{[]string{"--if-match", "bad\nheader"}, false},
		{[]string{"--if-none-match", "*", "--resume", uuid.NewString()}, false},
	} {
		if _, err := parseBucketTransfer(append(append([]string{}, base...), tc.flags...)); (err == nil) != tc.valid {
			t.Fatal(tc, err)
		}
	}
}
