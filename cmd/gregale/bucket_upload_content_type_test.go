package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// prod 2026-11-19: uploads were stored as application/octet-stream, so a
// public bucket served .css and .js that browsers refuse under nosniff.
func TestBucketUploadInfersContentTypeFromExtension(t *testing.T) {
	const bucket = "4c4d4dee-27b9-4f22-85fa-3a89c9b42680"
	for _, tc := range []struct {
		key, file, flag, want string
	}{
		{"site/app.css", "local.bin", "", "text/css"},
		{"app.js", "app.js", "", "javascript"},
		{"blob", "photo.PNG", "", "image/png"},
		{"data.unknownext", "x", "", "application/octet-stream"},
		{"app.css", "app.css", "text/plain", "text/plain"},
	} {
		args := []string{"upload", "my-app", bucket, tc.key, tc.file}
		if tc.flag != "" {
			args = append(args, "--content-type", tc.flag)
		}
		got, err := main.BucketUploadContentType(args)
		if err != nil || !strings.Contains(got, tc.want) {
			t.Fatalf("%s/%s: content type %q (err %v), want %q", tc.key, tc.file, got, err, tc.want)
		}
	}
}
