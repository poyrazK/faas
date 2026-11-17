// adr: 20261030125501 §4 — gregale_github_subscribe subscribes only for the
// app's own credential; it answers its call at once as a subscription, and a
// forwarded GitHub event reaches each subscribed thread once, as a RAP
// subscription event of the call, while revoked threads and other
// repositories receive nothing.
package agents_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// fakeGitHubSubscriptions keeps subscriptions in memory, as the store does:
// one per tool call, at most max live per endpoint.
type fakeGitHubSubscriptions struct {
	subs []agentstore.GitHubSubscription
	err  error
}

func (f *fakeGitHubSubscriptions) SubscribeGitHub(_ context.Context, sub agentstore.GitHubSubscription, now time.Time, max int) (agentstore.GitHubSubscription, bool, error) {
	if f.err != nil {
		return agentstore.GitHubSubscription{}, false, f.err
	}
	live := 0
	for _, s := range f.subs {
		if s.EndpointID == sub.EndpointID && s.ThreadID == sub.ThreadID && s.ToolCallID == sub.ToolCallID {
			return s, true, nil
		}
		if s.EndpointID == sub.EndpointID && s.ExpiresAt.After(now) {
			live++
		}
	}
	if live >= max {
		return agentstore.GitHubSubscription{}, false, &agentstore.LimitError{Limit: "AgentGitHubSubscriptionsPerEndpoint", Max: max, Observed: live}
	}
	sub.ID = "sub-" + sub.ThreadID + "-" + sub.ToolCallID
	f.subs = append(f.subs, sub)
	return sub, false, nil
}

func (f *fakeGitHubSubscriptions) GitHubSubscribers(_ context.Context, accountID, repository, eventType string, now time.Time, limit int) ([]agentstore.GitHubSubscription, error) {
	var out []agentstore.GitHubSubscription
	for _, s := range f.subs {
		if s.AccountID == accountID && s.Repository == repository && slices.Contains(s.EventTypes, eventType) && s.ExpiresAt.After(now) && len(out) < limit {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeGitHubSubscriptions) DeleteExpiredGitHubSubscriptions(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func newSubscribeFixture(t *testing.T) (*callbackFixture, *fakeGitHubSubscriptions) {
	t.Helper()
	f, _ := newToolFixture(t)
	subs := &fakeGitHubSubscriptions{}
	f.h.GitHubSubscriptions = subs
	return f, subs
}

func subscribeInvocation(token, thread, id string, args any) map[string]any {
	inv := invocation(token, thread, id, args)
	inv["operation"] = "gregale_github_subscribe"
	return inv
}

// A subscription reads the account's GitHub events, so a callback URL alone
// subscribes nothing.
func TestGitHubSubscribeNeedsTheAppsOwnCredential(t *testing.T) {
	f, subs := newSubscribeFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	args := map[string]any{"repository": "acme/app"}
	otherApp := agents.CallbackCaller{Account: keyCaller(callbackAccount).Account, AppID: "app-2"}
	for _, tc := range []struct {
		name   string
		caller *agents.CallbackCaller
		want   int
	}{
		{"the callback URL alone", nil, http.StatusUnauthorized},
		{"another app's workload token", &otherApp, http.StatusNotFound},
	} {
		if code := f.invokeAs(t, subscribeInvocation(token, "t-1", "c", args), tc.caller); code != tc.want {
			t.Errorf("%s: invoke = %d, want %d", tc.name, code, tc.want)
		}
	}
	if len(subs.subs) != 0 || len(f.inbox.messages) != 0 {
		t.Fatalf("a refused invocation subscribed %+v or published %+v", subs.subs, f.inbox.messages)
	}
}

func TestGitHubSubscribeRecordsItsCallAndAnswersAsASubscription(t *testing.T) {
	f, subs := newSubscribeFixture(t)
	issued := f.now
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	caller := keyCaller(callbackAccount)
	args := map[string]any{"repository": " Acme/App ", "events": []string{"opened", "synchronize", "opened"}}
	if code := f.invokeAs(t, subscribeInvocation(token, "t-1", "call_1", args), &caller); code != http.StatusOK {
		t.Fatalf("invoke = %d", code)
	}
	if len(subs.subs) != 1 {
		t.Fatalf("subscriptions %+v", subs.subs)
	}
	s := subs.subs[0]
	if s.AccountID != callbackAccount || s.EndpointID != "ep-1" || s.ThreadID != "t-1" || s.ToolCallID != "call_1" || s.Repository != "acme/app" ||
		!slices.Equal(s.EventTypes, []string{"pull_request.opened", "pull_request.synchronize"}) || !s.IssuedAt.Equal(issued) ||
		!s.ExpiresAt.Equal(f.now.Add(time.Duration(api.AgentGitHubSubscriptionMaxSeconds)*time.Second)) {
		t.Fatalf("subscription %+v", s)
	}
	if len(f.inbox.messages) != 1 {
		t.Fatalf("inbox %+v", f.inbox.messages)
	}
	m := f.inbox.messages[0]
	var result struct {
		Type, Text   string
		Subscription bool
	}
	if err := json.Unmarshal(m.Data, &result); err != nil || result.Type != "tool_result" || !result.Subscription ||
		!strings.HasPrefix(result.Text, "Subscribed to pull_request.opened, pull_request.synchronize on acme/app until ") ||
		m.Metadata["message_id"] != "rap-tool-result-call_1" {
		t.Fatalf("answer %s %v", m.Data, m.Metadata)
	}
	// A retried invocation finds its subscription and answers the same.
	if code := f.invokeAs(t, subscribeInvocation(token, "t-1", "call_1", args), &caller); code != http.StatusOK || len(subs.subs) != 1 || len(f.inbox.messages) != 1 {
		t.Fatalf("a retry: %d, %d subscriptions, %d messages", code, len(subs.subs), len(f.inbox.messages))
	}
}

// Refusals the agent can act on reach it as the call's result; a store
// failure is a 5xx, which the runtime retries.
func TestGitHubSubscribeRefusalsAreToolResults(t *testing.T) {
	f, subs := newSubscribeFixture(t)
	token, _ := f.issue(t, callbackAccount, api.CreateAgentCallbackRequest{EndpointID: "ep-1", ThreadID: "t-1"})
	caller := keyCaller(callbackAccount)
	for i := 0; i < api.AgentGitHubSubscriptionsPerEndpoint; i++ {
		subs.subs = append(subs.subs, agentstore.GitHubSubscription{EndpointID: "ep-1", ThreadID: "other", ToolCallID: "x" + string(rune('A'+i%26)) + string(rune('a'+i/26)),
			ExpiresAt: f.now.Add(time.Hour)})
	}
	full := len(subs.subs)
	for i, tc := range []struct {
		name string
		args any
		want string
	}{
		{"no repository", map[string]any{}, "gregale_github_subscribe: repository must be owner/name"},
		{"an unknown argument", map[string]any{"repository": "acme/app", "branch": "main"}, "gregale_github_subscribe: arguments must be"},
		{"an unknown action", map[string]any{"repository": "acme/app", "events": []string{"labeled"}}, "gregale_github_subscribe: events must be pull_request actions"},
		{"a past until", map[string]any{"repository": "acme/app", "until": f.now.Add(-time.Hour).Format(time.RFC3339)}, "gregale_github_subscribe: until must be"},
		{"an until past 30 days", map[string]any{"repository": "acme/app", "until": f.now.Add(31 * 24 * time.Hour).Format(time.RFC3339)}, "gregale_github_subscribe: until must be"},
		{"a full inbox", map[string]any{"repository": "acme/app"}, "gregale_github_subscribe: this inbox has 64 live subscriptions"},
	} {
		if code := f.invokeAs(t, subscribeInvocation(token, "t-1", "c"+string(rune('0'+i)), tc.args), &caller); code != http.StatusOK {
			t.Errorf("%s: invoke = %d", tc.name, code)
			continue
		}
		if len(f.inbox.messages) != i+1 || !strings.HasPrefix(resultText(t, f.inbox.messages[i]), tc.want) {
			t.Errorf("%s: inbox %d, want a result starting %q", tc.name, len(f.inbox.messages), tc.want)
		}
	}
	if len(subs.subs) != full {
		t.Fatalf("a refusal subscribed: %d subscriptions", len(subs.subs))
	}
	subs.err = errors.New("connection refused")
	if code := f.invokeAs(t, subscribeInvocation(token, "t-1", "c9", map[string]any{"repository": "acme/app"}), &caller); code != http.StatusInternalServerError {
		t.Fatalf("a store failure: invoke = %d, want 500 so the runtime retries", code)
	}
}

func pullRequestEvent(repository, action string) api.GitHubPullRequestEvent {
	return api.GitHubPullRequestEvent{Action: action, InstallationID: 7, Repository: api.GitHubEventRepository{FullName: repository},
		PullRequest: api.GitHubEventPullRequest{Number: 12, Title: "Fix the build", State: "open",
			Head: api.GitHubEventRef{Ref: "fix", SHA: strings.Repeat("a", 40)}, Base: api.GitHubEventRef{Ref: "main", SHA: strings.Repeat("b", 40)}}}
}

// A forwarded event reaches each subscribed thread once, as a subscription
// event of its call; other repositories and types, expired subscriptions and
// revoked threads receive nothing, and a failed publish is an error githubd
// retries.
func TestGitHubEventReachesSubscribedThreadsOnce(t *testing.T) {
	f, subs := newSubscribeFixture(t)
	issued := f.now
	f.now = f.now.Add(time.Second)
	if code := f.revoke(t, callbackAccount, "ep-1", "t-revoked"); code != http.StatusOK {
		t.Fatalf("revoke = %d", code)
	}
	sub := func(thread, call, repository string, expires time.Time, types ...string) agentstore.GitHubSubscription {
		return agentstore.GitHubSubscription{ID: "sub-" + thread, AccountID: callbackAccount, EndpointID: "ep-1", ThreadID: thread, ToolCallID: call,
			Repository: repository, EventTypes: types, IssuedAt: issued, ExpiresAt: expires}
	}
	later := f.now.Add(time.Hour)
	subs.subs = []agentstore.GitHubSubscription{
		sub("t-1", "call_1", "acme/app", later, "pull_request.opened"),
		sub("t-2", "call_2", "acme/app", later, "pull_request.opened", "pull_request.closed"),
		sub("t-other-repo", "c", "acme/lib", later, "pull_request.opened"),
		sub("t-other-type", "c", "acme/app", later, "pull_request.closed"),
		sub("t-expired", "c", "acme/app", f.now.Add(-time.Second), "pull_request.opened"),
		sub("t-revoked", "c", "acme/app", later, "pull_request.opened"),
	}
	const delivery = "0f5c2f8e-1d2b-4c3a-9e8f-7a6b5c4d3e2f"
	event := pullRequestEvent("Acme/App", "opened")
	for range 2 {
		if err := f.h.DeliverGitHubEvent(context.Background(), callbackAccount, delivery, event); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.inbox.messages) != 2 {
		t.Fatalf("inbox %+v", f.inbox.messages)
	}
	for i, want := range []struct{ thread, call string }{{"t-1", "call_1"}, {"t-2", "call_2"}} {
		m := f.inbox.messages[i]
		var got struct {
			Type       string
			GroupID    string `json:"group_id"`
			ToolCallID string `json:"tool_call_id"`
			Text       string
		}
		if err := json.Unmarshal(m.Data, &got); err != nil {
			t.Fatal(err)
		}
		var text struct {
			Type       string
			DeliveryID string `json:"delivery_id"`
			Event      api.GitHubPullRequestEvent
		}
		if m.Metadata["thread_id"] != want.thread || m.Metadata["kind"] != api.AgentCallbackKindEvent || got.Type != "subscription_event" ||
			got.GroupID != want.thread || got.ToolCallID != want.call || json.Unmarshal([]byte(got.Text), &text) != nil ||
			text.Type != "pull_request.opened" || text.DeliveryID != delivery || text.Event.PullRequest.Number != 12 {
			t.Fatalf("message %d: %v %s", i, m.Metadata, m.Data)
		}
	}

	f.h.Inbox = failingInbox{f.inbox}
	if err := f.h.DeliverGitHubEvent(context.Background(), callbackAccount, "1f5c2f8e-1d2b-4c3a-9e8f-7a6b5c4d3e2f", event); err == nil {
		t.Fatal("a failed publish was not reported")
	}
	f.h.GitHubSubscriptions = nil
	if err := f.h.DeliverGitHubEvent(context.Background(), callbackAccount, delivery, event); err != nil {
		t.Fatalf("without subscriptions: %v", err)
	}
}
