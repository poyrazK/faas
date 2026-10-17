package issueslack

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type interactionReplyTransport func(*http.Request) (*http.Response, error)

func (f interactionReplyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestInteractionResponsesPinSlackDestination(t *testing.T) {
	valid := "https://hooks.slack.com/actions/T0123456789/123456789/response-token"
	calls := 0
	client := NewClient(&http.Client{Transport: interactionReplyTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != valid || r.Header.Get("Authorization") != "" {
			t.Fatal("untrusted response destination")
		}
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), `"response_type":"ephemeral"`) || !strings.Contains(string(raw), `"replace_original":false`) {
			t.Fatal(string(raw))
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://attacker.invalid"}}, Body: io.NopCloser(strings.NewReader("redirect"))}, nil
	})})
	if err := client.RespondToInteraction(context.Background(), valid, "T0123456789", "Confirm account"); err == nil || calls != 1 {
		t.Fatal("redirect followed", calls, err)
	}
	for _, bad := range []string{"http://hooks.slack.com/actions/T0123456789/a/b", "https://hooks.slack.com.evil/actions/T0123456789/a/b", "https://user@hooks.slack.com/actions/T0123456789/a/b", "https://hooks.slack.com:443/actions/T0123456789/a/b", "https://hooks.slack.com/actions/T9876543210/a/b", "https://hooks.slack.com/actions/T0123456789/a/b?next=evil", "https://hooks.slack.com/actions/T0123456789/a/b#", "https://hooks.slack.com/actions/T0123456789/%2e%2e/token", "https://hooks.slack.com/services/T0123456789/a/b"} {
		if err := client.RespondToInteraction(context.Background(), bad, "T0123456789", "text"); err == nil {
			t.Fatal(bad)
		}
	}
	if calls != 1 {
		t.Fatal("unsafe URL made a network call")
	}
}
