package api

import (
	"strings"
	"testing"
)

func TestIssueTrackerLinkCanonicalizationAndValidation(t *testing.T) {
	for _, tc := range []struct{ url, provider, key, want string }{
		{" https://GitHub.com/ACME/Payments/issues/00042?utm_source=app#issuecomment-1 ", "github", "acme/payments#42", "https://github.com/acme/payments/issues/42"},
		{"https://linear.app/ACME/issue/pay-123/old-title?filter=all#note", "linear", "acme/PAY-123", "https://linear.app/acme/issue/PAY-123"},
	} {
		got, err := NormalizeIssueTrackerLink(PutIssueTrackerLinkRequest{URL: tc.url, Title: " Title "})
		if err != nil || got.Provider != tc.provider || got.ExternalKey != tc.key || got.URL != tc.want || got.Title != "Title" {
			t.Fatalf("%s: %+v %v", tc.url, got, err)
		}
	}
	for _, raw := range []string{"javascript:alert(1)", "http://github.com/a/b/issues/1", "https://github.com.evil.test/a/b/issues/1", "https://secret@github.com/a/b/issues/1", "https://github.com:8443/a/b/issues/1", "https://github.com/a/b/pull/1", "https://github.com/a/b/issues/0", "https://github.com/a/b/issues/+1", "https://github.com/a/../issues/1", "https://github.com/a/b/issues/1%2Fextra", "https://linear.app/acme/issue/123", "https://linear.app/acme/issue/PAY-0", strings.Repeat("x", IssueTrackerLinkURLMaxBytes+1)} {
		if _, err := NormalizeIssueTrackerLink(PutIssueTrackerLinkRequest{URL: raw}); err == nil {
			t.Fatal("invalid tracker URL accepted", raw)
		}
	}
	if _, err := NormalizeIssueTrackerLink(PutIssueTrackerLinkRequest{URL: "https://github.com/a/b/issues/1", Title: "hidden\x00control"}); err == nil {
		t.Fatal("title control accepted")
	}
}
