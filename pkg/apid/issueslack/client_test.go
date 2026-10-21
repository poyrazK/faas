package issueslack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhook"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestSlackClientThreadsSafeMessagesAndClassifiesFailures(t *testing.T) {
	cases := []struct {
		name                                string
		code                                int
		body, after, status, reason, thread string
		reset                               bool
	}{
		{name: "root", code: 200, body: `{"ok":true,"channel":"C0123456789","ts":"123.456"}`, status: "succeeded"},
		{name: "reply", code: 200, body: `{"ok":true,"channel":"C0123456789","ts":"123.457"}`, status: "succeeded", thread: "123.456"},
		{name: "rate limit", code: 429, after: "120", status: "failed", reason: "rate_limited"},
		{name: "receiver unavailable", code: 503, status: "failed", reason: "slack_unavailable"},
		{name: "redirect rejected", code: 302, status: "dead", reason: "slack_rejected"},
		{name: "invalid credentials", code: 200, body: `{"ok":false,"error":"invalid_auth"}`, status: "dead", reason: "invalid_auth"},
		{name: "channel", code: 200, body: `{"ok":false,"error":"channel_not_found"}`, status: "dead", reason: "channel_not_found"},
		{name: "join required", code: 200, body: `{"ok":false,"error":"not_in_channel"}`, status: "dead", reason: "not_in_channel"},
		{name: "scope", code: 200, body: `{"ok":false,"error":"missing_scope"}`, status: "dead", reason: "missing_scope"},
		{name: "unknown error redacted", code: 200, body: `{"ok":false,"error":"xoxb-do-not-persist"}`, status: "dead", reason: "slack_rejected"},
		{name: "bad json", code: 200, body: `secret invalid response`, status: "failed", reason: "invalid_response"},
		{name: "oversized response", code: 200, body: strings.Repeat("x", api.IssueSlackResponseMaxBytes+1), status: "failed", reason: "invalid_response"},
		{name: "bad timestamp", code: 200, body: `{"ok":true,"channel":"C0123456789","ts":"<script>"}`, status: "failed", reason: "invalid_response"},
		{name: "wrong channel", code: 200, body: `{"ok":true,"channel":"C9999999999","ts":"123.456"}`, status: "failed", reason: "invalid_response"},
		{name: "missing thread", code: 200, body: `{"ok":false,"error":"thread_not_found"}`, status: "failed", reason: "thread_unavailable", thread: "123.456", reset: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer xoxb-fake-token" {
					t.Error("request authentication or method")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["channel"] != "C0123456789" || body["client_msg_id"] != "delivery-id" || body["parse"] != "none" || body["unfurl_links"] != false {
					t.Error(body)
				}
				if tc.thread != "" && body["thread_ts"] != tc.thread || tc.thread == "" && body["thread_ts"] != nil {
					t.Error("thread", body)
				}
				encoded, _ := json.Marshal(body)
				if strings.Contains(string(encoded), "xoxb-") || strings.Contains(string(encoded), "<@everyone>") || strings.Contains(string(encoded), `"type":"actions"`) {
					t.Error("secret or unsafe reason in message")
				}
				w.Header().Set("Retry-After", tc.after)
				w.Header().Set("Location", "https://untrusted.example/leak")
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			target, _ := url.Parse(server.URL)
			client := NewClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != "https://slack.com/api/chat.postMessage" {
					t.Fatal("nonfixed provider URL", r.URL)
				}
				copy := r.Clone(r.Context())
				u := *r.URL
				u.Scheme, u.Host = target.Scheme, target.Host
				copy.URL = &u
				return server.Client().Transport.RoundTrip(copy)
			})})
			start := time.Now()
			result := client.Send(t.Context(), state.IssueSlackClaim{ID: "delivery-id", NotificationID: "notice", ChannelID: "C0123456789", AppSlug: "app", IncidentID: "incident", Kind: "reminder", Reason: "<@everyone>", Attempt: 1, ThreadTS: tc.thread}, "xoxb-fake-token", "https://gregale.example")
			if result.Status != tc.status || result.Error != tc.reason || result.ResetThread != tc.reset || calls != 1 {
				t.Fatal(result, calls)
			}
			if tc.after == "120" && result.RetryAt.Before(start.Add(119*time.Second)) {
				t.Fatal("Retry-After ignored", result)
			}
		})
	}
}
func TestSlackRetryBudgetTokenAndOriginValidation(t *testing.T) {
	if api.IssueSlackMaxAttempts != len(webhook.DefaultBackoffs[state.AppWebhookRetryDefault])+1 {
		t.Fatal("Slack and webhook retry budgets differ")
	}
	r := retry(state.IssueSlackClaim{Attempt: api.IssueSlackMaxAttempts}, "transport_error", 0, "")
	if r.Status != "dead" {
		t.Fatal(r)
	}
	limited := retry(state.IssueSlackClaim{Attempt: api.IssueSlackMaxAttempts}, "rate_limited", 429, "120")
	if limited.Status != "dead" || limited.RetryAt.Before(time.Now().Add(119*time.Second)) {
		t.Fatal("exhaustion dropped rate-limit cooldown", limited)
	}

	now := time.Now()
	if until := slackRetryAfter("999999999", now); !until.Equal(now.Add(api.IssueSlackDeliveryTTL)) {
		t.Fatal(until)
	}
	if !slackRetryAfter("malformed", now).IsZero() {
		t.Fatal("invalid Retry-After accepted")
	}
	c := state.IssueSlackClaim{Kind: "failure", AppSlug: "app", ChannelID: "C0123456789"}
	for _, base := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com/?secret=x", "//example.com"} {
		if _, err := message(c, base); err == nil {
			t.Fatal("unsafe origin", base)
		}
	}
	calls := 0
	client := NewClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("secret must not be persisted")
	})})
	if r := client.Send(t.Context(), c, "bad-token", "https://gregale.example"); r.Error != "token_unavailable" || calls != 0 {
		t.Fatal(r, calls)
	}
	c.Attempt = 1
	if r := client.Send(t.Context(), c, "xoxb-fake-token", "https://gregale.example"); r.Error != "transport_error" || r.Status != "failed" {
		t.Fatal(r)
	}
}

type fakeStore struct {
	state.IssueSlackStore
	claims    []state.IssueSlackClaim
	calls     int
	ready     bool
	result    state.IssueSlackResult
	finishErr error
}

func (s *fakeStore) ClaimIssueSlackDelivery(context.Context) (state.IssueSlackClaim, bool, error) {
	s.calls++
	if len(s.claims) == 0 {
		return state.IssueSlackClaim{}, false, nil
	}
	c := s.claims[0]
	s.claims = s.claims[1:]
	return c, true, nil
}
func (s *fakeStore) IssueSlackDeliveryReady(context.Context, state.IssueSlackClaim) (bool, error) {
	return s.ready, nil
}
func (s *fakeStore) FinishIssueSlackDelivery(_ context.Context, _ state.IssueSlackClaim, r state.IssueSlackResult) error {
	s.result = r
	return s.finishErr
}
func TestSlackWorkerBoundsCancellationAndEligibility(t *testing.T) {
	s := &fakeStore{}
	for range api.IssueSlackBatch + 2 {
		s.claims = append(s.claims, state.IssueSlackClaim{Status: "canceled"})
	}
	w := Worker{Store: s}
	if err := w.Batch(t.Context()); err != nil || s.calls != api.IssueSlackBatch {
		t.Fatal(err, s.calls)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := w.Batch(ctx); !errors.Is(err, context.Canceled) || s.calls != api.IssueSlackBatch {
		t.Fatal(err, s.calls)
	}
	s = &fakeStore{claims: []state.IssueSlackClaim{{Status: "in_flight"}}, finishErr: state.ErrIssueSlackClaimLost}
	w = Worker{Store: s, OpenToken: func([]byte) (string, error) { return "xoxb-fake-token", nil }}
	if ok, err := w.Step(t.Context()); err != nil || !ok || s.result.Status != "canceled" {
		t.Fatal(ok, err, s.result)
	}
	s = &fakeStore{claims: []state.IssueSlackClaim{{Status: "in_flight"}}}
	w = Worker{Store: s, OpenToken: func([]byte) (string, error) { return "", errors.New("secret decryption failure") }}
	if ok, err := w.Step(t.Context()); err != nil || !ok || s.result.Error != "token_unavailable" {
		t.Fatal(ok, err, s.result)
	}
}
