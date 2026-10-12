package s3gateway

import "testing"

// adr: 1034
func TestParsePathBucketTrailingSlash(t *testing.T) {
	for _, tc := range []struct {
		path, bucket, key string
		hasKey, invalid   bool
	}{
		{path: "/assets", bucket: "assets"},
		{path: "/assets/", bucket: "assets"}, // AWS JavaScript v3's bucket URI.
		{path: "/assets/file.bin", bucket: "assets", key: "file.bin", hasKey: true},
		{path: "/assets/folder/", bucket: "assets", key: "folder/", hasKey: true},
		{path: "/assets/%2F", bucket: "assets", key: "/", hasKey: true},
		{path: "/assets//", bucket: "assets", key: "/", hasKey: true},
		{path: "//", invalid: true},
		{path: "/assets%2F/", invalid: true},
		{path: "/assets/%ZZ", invalid: true},
	} {
		t.Run(tc.path, func(t *testing.T) {
			bucket, key, hasBucket, hasKey, err := parsePath(tc.path)
			if (err != nil) != tc.invalid {
				t.Fatalf("parsePath invalid = %v, want %v", err, tc.invalid)
			}
			if !tc.invalid && (bucket != tc.bucket || key != tc.key || !hasBucket || hasKey != tc.hasKey) {
				t.Fatalf("parsePath = %q %q %v %v", bucket, key, hasBucket, hasKey)
			}
		})
	}
}
