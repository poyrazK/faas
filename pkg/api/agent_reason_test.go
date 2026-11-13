package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSanitizeAgentReason(t *testing.T) {
	long := strings.Repeat("é", api.MaxAgentReasonChars+20)
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"spaces only", " \t\n ", ""},
		{"trimmed", "  roll back the bad release  ", "roll back the bad release"},
		{"control chars become spaces", "line one\r\nline two\x00\x1b[31mred", "line one line two [31mred"},
		{"format chars dropped to spaces", "a‮b​c", "a b c"},
		{"invalid utf8 dropped", "ok\xffay", "okay"},
		{"collapsed", "a    b\t\tc", "a b c"},
		{"cut at limit", long, strings.Repeat("é", api.MaxAgentReasonChars)},
		{"no trailing space at cut", strings.Repeat("a", api.MaxAgentReasonChars-1) + " bbb", strings.Repeat("a", api.MaxAgentReasonChars-1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := api.SanitizeAgentReason(tc.in)
			if got != tc.want {
				t.Fatalf("SanitizeAgentReason(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if n := utf8.RuneCountInString(got); n > api.MaxAgentReasonChars {
				t.Fatalf("got %d characters, limit %d", n, api.MaxAgentReasonChars)
			}
		})
	}
}

func TestClientSendsContextAgentReason(t *testing.T) {
	got := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get(api.AgentReasonHeader)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()
	client := api.NewClient(srv.URL, "fp_test")
	ctx := api.WithAgentReason(context.Background(), "  fix the\ncheckout outage ")
	if _, err := client.ListAgentApprovals(ctx); err != nil {
		t.Fatalf("ListAgentApprovals: %v", err)
	}
	if reason := <-got; reason != "fix the checkout outage" {
		t.Fatalf("header = %q, want the sanitised reason", reason)
	}
	if _, err := client.ListAgentApprovals(context.Background()); err != nil {
		t.Fatalf("ListAgentApprovals: %v", err)
	}
	if reason := <-got; reason != "" {
		t.Fatalf("header without a reason = %q, want none", reason)
	}
}
