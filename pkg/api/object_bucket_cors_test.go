// adr: 20261025202805

package api_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestValidObjectBucketCORSRules(t *testing.T) {
	ok := api.ObjectBucketCORSRule{AllowedOrigins: []string{"https://*.example.test"}, AllowedMethods: []string{"GET", "PUT"}, AllowedHeaders: []string{"x-amz-*"}, ExposeHeaders: []string{"ETag"}, MaxAgeSeconds: 600}
	if !api.ValidObjectBucketCORSRules([]api.ObjectBucketCORSRule{ok}) {
		t.Fatal("valid rule refused")
	}
	if !api.ValidObjectBucketCORSRules([]api.ObjectBucketCORSRule{{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"HEAD"}}}) {
		t.Fatal("any-origin rule refused")
	}
	for name, change := range map[string]func(*api.ObjectBucketCORSRule){
		"no origin":        func(r *api.ObjectBucketCORSRule) { r.AllowedOrigins = nil },
		"two wildcards":    func(r *api.ObjectBucketCORSRule) { r.AllowedOrigins = []string{"https://*.*.example.test"} },
		"origin with path": func(r *api.ObjectBucketCORSRule) { r.AllowedOrigins = []string{"https://example.test/app"} },
		"ftp origin":       func(r *api.ObjectBucketCORSRule) { r.AllowedOrigins = []string{"ftp://example.test"} },
		"unknown method":   func(r *api.ObjectBucketCORSRule) { r.AllowedMethods = []string{"PATCH"} },
		"no method":        func(r *api.ObjectBucketCORSRule) { r.AllowedMethods = nil },
		"bad header":       func(r *api.ObjectBucketCORSRule) { r.AllowedHeaders = []string{"bad header"} },
		"wildcard exposed": func(r *api.ObjectBucketCORSRule) { r.ExposeHeaders = []string{"x-amz-*"} },
		"negative max age": func(r *api.ObjectBucketCORSRule) { r.MaxAgeSeconds = -1 },
		"max age too long": func(r *api.ObjectBucketCORSRule) { r.MaxAgeSeconds = api.MaxObjectBucketCORSMaxAgeSeconds + 1 },
		"rule ID too long": func(r *api.ObjectBucketCORSRule) { r.ID = strings.Repeat("a", api.MaxObjectBucketCORSRuleIDBytes+1) },
		"too many origins": func(r *api.ObjectBucketCORSRule) {
			r.AllowedOrigins = make([]string, api.MaxObjectBucketCORSRuleValues+1)
		},
	} {
		r := ok
		change(&r)
		if api.ValidObjectBucketCORSRules([]api.ObjectBucketCORSRule{r}) {
			t.Fatal(name, "accepted")
		}
	}
	if api.ValidObjectBucketCORSRules(nil) || api.ValidObjectBucketCORSRules(make([]api.ObjectBucketCORSRule, api.MaxObjectBucketCORSRules+1)) {
		t.Fatal("rule count unbounded")
	}
	dup := ok
	dup.ID = "same"
	if api.ValidObjectBucketCORSRules([]api.ObjectBucketCORSRule{dup, dup}) {
		t.Fatal("duplicate rule IDs accepted")
	}
}

func TestMatchObjectBucketCORS(t *testing.T) {
	rules := []api.ObjectBucketCORSRule{
		{ID: "uploads", AllowedOrigins: []string{"https://*.example.test"}, AllowedMethods: []string{"PUT"}, AllowedHeaders: []string{"Content-Type", "x-amz-*"}},
		{ID: "reads", AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}},
	}
	for _, tc := range []struct {
		origin, method string
		headers        []string
		want           string
	}{
		{"https://app.example.test", "PUT", []string{"content-type", "x-amz-checksum-crc32"}, "uploads"},
		{"https://app.example.test", "PUT", []string{"authorization"}, ""},
		{"https://example.test", "PUT", nil, ""},
		{"https://anything.test", "GET", nil, "reads"},
		{"https://anything.test", "GET", []string{"range"}, ""},
		{"https://app.example.test", "DELETE", nil, ""},
	} {
		rule, ok := api.MatchObjectBucketCORS(rules, tc.origin, tc.method, tc.headers)
		if ok != (tc.want != "") || rule.ID != tc.want {
			t.Errorf("%s %s %v matched %q, want %q", tc.method, tc.origin, tc.headers, rule.ID, tc.want)
		}
	}
}
