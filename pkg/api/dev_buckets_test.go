package api

import (
	"strings"
	"testing"
)

func TestNormalizeDevBuckets(t *testing.T) {
	for _, tc := range []struct {
		name     string
		requests []DevBucketRequest
		valid    bool
	}{
		{"omitted", nil, true},
		{"defaults", []DevBucketRequest{{Name: "assets"}}, true},
		{"custom", []DevBucketRequest{{Name: "assets", Prefix: "FILES", Permission: "read", Region: "us-east-1"}}, true},
		{"invalid name", []DevBucketRequest{{Name: "../assets"}}, false},
		{"invalid permission", []DevBucketRequest{{Name: "assets", Permission: "admin"}}, false},
		{"invalid prefix", []DevBucketRequest{{Name: "assets", Prefix: "assets"}}, false},
		{"duplicate name", []DevBucketRequest{{Name: "assets"}, {Name: "assets", Prefix: "OTHER"}}, false},
		{"duplicate prefix", []DevBucketRequest{{Name: "a", Prefix: "FILES"}, {Name: "b", Prefix: "FILES"}}, false},
		{"truncated prefix collision", []DevBucketRequest{{Name: strings.Repeat("a", 50) + "b"}, {Name: strings.Repeat("a", 50) + "c"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeDevBuckets(tc.requests)
			if (err == nil) != tc.valid {
				t.Fatalf("validation = %v", err)
			}
			if tc.name == "defaults" && (got[0].Prefix != "GREGALE_S3_ASSETS" || got[0].Permission != "read_write" || tc.requests[0].Prefix != "") {
				t.Fatalf("defaults or caller mutation: %+v %+v", got, tc.requests)
			}
		})
	}
}
