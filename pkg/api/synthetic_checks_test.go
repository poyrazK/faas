package api_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNormalizeCreateSyntheticCheck(t *testing.T) {
	valid := api.CreateSyntheticCheckRequest{Name: "health", Path: "/healthz", IntervalSeconds: 300}
	tests := []struct {
		name   string
		mutate func(*api.CreateSyntheticCheckRequest)
		ok     bool
	}{
		{"defaults", func(*api.CreateSyntheticCheckRequest) {}, true},
		{"root path", func(r *api.CreateSyntheticCheckRequest) { r.Path = "/" }, true},
		{"query string", func(r *api.CreateSyntheticCheckRequest) { r.Path = "/api/ping?deep=1" }, true},
		{"head lowercase", func(r *api.CreateSyntheticCheckRequest) { r.Method = "head" }, true},
		{"exact status", func(r *api.CreateSyntheticCheckRequest) { r.ExpectedStatus = 204 }, true},
		{"protocol-relative host", func(r *api.CreateSyntheticCheckRequest) { r.Path = "//evil.example/x" }, false},
		{"absolute URL", func(r *api.CreateSyntheticCheckRequest) { r.Path = "https://evil.example/" }, false},
		{"relative path", func(r *api.CreateSyntheticCheckRequest) { r.Path = "healthz" }, false},
		{"backslash", func(r *api.CreateSyntheticCheckRequest) { r.Path = `/\evil.example` }, false},
		{"whitespace", func(r *api.CreateSyntheticCheckRequest) { r.Path = "/a b" }, false},
		{"too long", func(r *api.CreateSyntheticCheckRequest) { r.Path = "/" + strings.Repeat("a", 512) }, false},
		{"POST", func(r *api.CreateSyntheticCheckRequest) { r.Method = "POST" }, false},
		{"one-minute interval", func(r *api.CreateSyntheticCheckRequest) { r.IntervalSeconds = 60 }, false},
		{"timeout too long", func(r *api.CreateSyntheticCheckRequest) { r.TimeoutMS = 60000 }, false},
		{"bad status", func(r *api.CreateSyntheticCheckRequest) { r.ExpectedStatus = 700 }, false},
		{"bad name", func(r *api.CreateSyntheticCheckRequest) { r.Name = "Health" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := valid
			tt.mutate(&req)
			got, p := api.NormalizeCreateSyntheticCheck(req)
			if (p == nil) != tt.ok {
				t.Fatalf("problem = %+v, want ok=%v", p, tt.ok)
			}
			if tt.ok && (got.Method == "" || got.TimeoutMS != max(req.TimeoutMS, api.SyntheticCheckDefaultTimeoutMS)) {
				t.Fatalf("defaults not applied: %+v", got)
			}
		})
	}
}
