package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestValidateCronPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		ok   bool
	}{
		{"/", true},
		{"/jobs/nightly", true},
		{"/jobs/sync?full=1&since=2026-10-01", true},
		{"/a//b", true},
		{"/" + strings.Repeat("a", api.CronPathMaxBytes-1), true},
		{"", false},
		{"jobs", false},
		{"//evil.example/x", false},
		{"https://evil.example/x", false},
		{"/a b", false},
		{"/a\tb", false},
		{"/a\r\nHost: evil", false},
		{"/a\\b", false},
		{"/a#frag", false},
		{"/a\x00b", false},
		{"/a\x7fb", false},
		{"/" + strings.Repeat("a", api.CronPathMaxBytes), false},
	} {
		problem := api.ValidateCronPath(tc.path)
		if (problem == nil) != tc.ok {
			t.Errorf("ValidateCronPath(%q) = %+v, want ok=%v", tc.path, problem, tc.ok)
			continue
		}
		if problem != nil && (problem.Status != http.StatusBadRequest || problem.Code != api.CodeCronInvalid || problem.DocsURL == "") {
			t.Errorf("ValidateCronPath(%q) problem = %+v, want a documented 400 cron_invalid", tc.path, problem)
		}
	}
	long := api.ValidateCronPath("/" + strings.Repeat("a", api.CronPathMaxBytes))
	if long.Limit == nil || *long.Limit != api.CronPathMaxBytes || long.Observed == nil || *long.Observed != api.CronPathMaxBytes+1 {
		t.Fatalf("oversized path problem lacks limit and observed size: %+v", long)
	}
}
