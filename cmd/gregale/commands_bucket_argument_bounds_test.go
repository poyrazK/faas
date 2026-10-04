package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestBucketCommandsRejectInvalidArity(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "token")
	var stderr bytes.Buffer
	oldErr := osStderr
	osStderr = &stderr
	t.Cleanup(func() { osStderr = oldErr })
	bucket, request := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name string
		run  func([]string) int
		args []string
	}{
		{"deletion_start", cmdBucketDeletions, []string{"start", "demo", bucket, "key", request}},
		{"deletion_status", cmdBucketDeletions, []string{"status", "demo", bucket, request}},
		{"tag_set", cmdBucketTags, []string{"set", "demo", bucket, "key", "team=core"}},
		{"tag_get", cmdBucketTags, []string{"get", "demo", bucket, "key"}},
		{"notification_set", cmdBucketNotifications, []string{"set", "demo", bucket, "-"}},
		{"notification_get", cmdBucketNotifications, []string{"get", "demo", bucket}},
		{"encryption_kms", cmdBucketDefaultEncryption, []string{"aws:kms", "demo", bucket, "owned-key"}},
		{"encryption_status", cmdBucketDefaultEncryption, []string{"status", "demo", bucket}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for count := 0; count < len(tc.args); count++ {
				if code := tc.run(tc.args[:count]); code != 1 || calls != 0 {
					t.Fatalf("truncated arguments (%d) dispatched: code=%d calls=%d", count, code, calls)
				}
			}
			args := append(append([]string{}, tc.args...), "extra", "extra")
			if code := tc.run(args); code != 1 || calls != 0 {
				t.Fatalf("excess arguments dispatched: code=%d calls=%d", code, calls)
			}
		})
	}
}
