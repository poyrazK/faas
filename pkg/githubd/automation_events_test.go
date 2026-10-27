// adr: 20261027090010
package githubd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// gitHubPullRequestDelivery has the shape GitHub sends: the head SHA lives
// under pull_request.head, and there is no pull_request.head_sha.
func gitHubPullRequestDelivery(t *testing.T, action string, edit func(map[string]any)) []byte {
	t.Helper()
	body := map[string]any{
		"action": action,
		"number": 42,
		"pull_request": map[string]any{
			"number": 42, "title": "Fix the thing", "state": "open", "draft": false, "merged": false,
			"html_url": "https://github.com/octo/api/pull/42", "body": "a long description",
			"user":   map[string]any{"login": "octocat"},
			"labels": []any{map[string]any{"name": "bug"}, map[string]any{"name": ""}},
			"head":   map[string]any{"ref": "fix", "sha": strings.Repeat("A", 40), "repo": map[string]any{"full_name": "octo/api"}},
			"base":   map[string]any{"ref": "main", "sha": strings.Repeat("b", 40), "repo": map[string]any{"full_name": "octo/api"}},
		},
		"repository":   map[string]any{"full_name": "octo/api", "default_branch": "main", "private": true},
		"installation": map[string]any{"id": 7},
		"sender":       map[string]any{"login": "hubot"},
	}
	if edit != nil {
		edit(body)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func pullRequestOf(body map[string]any) map[string]any { return body["pull_request"].(map[string]any) }

func TestPullRequestAutomationEvent(t *testing.T) {
	event, ok, err := PullRequestAutomationEvent(gitHubPullRequestDelivery(t, "opened", nil))
	if err != nil || !ok {
		t.Fatalf("PullRequestAutomationEvent() = %v, %v", ok, err)
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("event does not validate: %v", err)
	}
	pr := event.PullRequest
	if event.Type() != "pull_request.opened" || event.InstallationID != 7 || event.Sender != "hubot" ||
		event.Repository != (api.GitHubEventRepository{FullName: "octo/api", DefaultBranch: "main", Private: true}) {
		t.Fatalf("event = %+v", event)
	}
	if pr.Number != 42 || pr.Head.SHA != strings.Repeat("a", 40) || pr.Base.SHA != strings.Repeat("b", 40) ||
		pr.Fork || pr.Author != "octocat" || len(pr.Labels) != 1 || pr.Labels[0] != "bug" || pr.URL == "" {
		t.Fatalf("pull request = %+v", pr)
	}
	data, _ := json.Marshal(event)
	if strings.Contains(string(data), "long description") {
		t.Fatalf("event carries the pull request body: %s", data)
	}

	if _, ok, err := PullRequestAutomationEvent(gitHubPullRequestDelivery(t, "labeled", nil)); ok || err != nil {
		t.Fatalf("labeled = %v, %v; want no event", ok, err)
	}
	if _, _, err := PullRequestAutomationEvent([]byte(`{`)); err == nil {
		t.Fatal("malformed body decoded")
	}
}

func TestPullRequestAutomationEventBounds(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(map[string]any)
		check func(*testing.T, api.GitHubPullRequestEvent)
	}{
		{"fork", func(b map[string]any) {
			pullRequestOf(b)["head"].(map[string]any)["repo"] = map[string]any{"full_name": "mallory/api"}
		}, func(t *testing.T, e api.GitHubPullRequestEvent) {
			if !e.PullRequest.Fork || e.PullRequest.Head.Repository != "mallory/api" {
				t.Fatalf("fork = %+v", e.PullRequest)
			}
		}},
		{"deleted fork", func(b map[string]any) {
			pullRequestOf(b)["head"].(map[string]any)["repo"] = nil
		}, func(t *testing.T, e api.GitHubPullRequestEvent) {
			if !e.PullRequest.Fork {
				t.Fatal("a missing head repository is not marked as a fork")
			}
		}},
		{"long title", func(b map[string]any) {
			pullRequestOf(b)["title"] = strings.Repeat("é", api.GitHubEventTitleMaxBytes)
		}, func(t *testing.T, e api.GitHubPullRequestEvent) {
			if len(e.PullRequest.Title) != api.GitHubEventTitleMaxBytes || e.Validate() != nil {
				t.Fatalf("title kept %d bytes, validate %v", len(e.PullRequest.Title), e.Validate())
			}
		}},
		{"many labels", func(b map[string]any) {
			labels := make([]any, api.GitHubEventLabelsMax+5)
			for i := range labels {
				labels[i] = map[string]any{"name": strings.Repeat("ü", api.GitHubEventLabelMaxBytes)}
			}
			pullRequestOf(b)["labels"] = labels
		}, func(t *testing.T, e api.GitHubPullRequestEvent) {
			if len(e.PullRequest.Labels) != api.GitHubEventLabelsMax || e.Validate() != nil {
				t.Fatalf("labels = %d, validate %v", len(e.PullRequest.Labels), e.Validate())
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event, ok, err := PullRequestAutomationEvent(gitHubPullRequestDelivery(t, "synchronize", tc.edit))
			if err != nil || !ok {
				t.Fatalf("PullRequestAutomationEvent() = %v, %v", ok, err)
			}
			tc.check(t, event)
		})
	}
}

type recordingAutomationEvents struct {
	events []AutomationEvent
	err    error
}

func (r *recordingAutomationEvents) PublishAutomationEvent(_ context.Context, e AutomationEvent) error {
	r.events = append(r.events, e)
	return r.err
}

func TestHandleWebhookDeliveryForwardsPullRequests(t *testing.T) {
	const deliveryID = "72D3162E-CC78-11E3-81AB-4C9367DC0958"
	cases := []struct {
		name      string
		action    string
		publisher *recordingAutomationEvents
		forwarded int
		wantErr   string
	}{
		{"opened", "opened", &recordingAutomationEvents{}, 1, ""},
		// The preview path refuses ready_for_review; the event is forwarded
		// before it does.
		{"ready for review", "ready_for_review", &recordingAutomationEvents{}, 1, ""},
		{"other action", "labeled", &recordingAutomationEvents{}, 0, ""},
		{"rejected", "opened", &recordingAutomationEvents{err: ErrAutomationEventRejected}, 1, ""},
		{"unavailable", "opened", &recordingAutomationEvents{err: errors.New("bridge down")}, 1, "bridge down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
			svc.AutomationEvents = tc.publisher
			err := svc.HandleWebhookDelivery(context.Background(), WebhookDelivery{DeliveryID: deliveryID,
				EventType: "pull_request", Payload: gitHubPullRequestDelivery(t, tc.action, nil)})
			// Only a forwarding failure a retry may fix is this path's error;
			// whatever the preview path then decides is its own.
			forwardErr := err != nil && strings.Contains(err.Error(), "forward pull_request")
			if tc.wantErr == "" && forwardErr || tc.wantErr != "" && (!forwardErr || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("HandleWebhookDelivery() = %v, want forwarding error %q", err, tc.wantErr)
			}
			if len(tc.publisher.events) != tc.forwarded {
				t.Fatalf("forwarded %d events, want %d", len(tc.publisher.events), tc.forwarded)
			}
			if tc.forwarded == 0 {
				return
			}
			got := tc.publisher.events[0]
			if got.DeliveryID != strings.ToLower(deliveryID) || got.InstallationID != 7 || got.Type != "pull_request."+tc.action {
				t.Fatalf("forwarded %+v", got)
			}
			var event api.GitHubPullRequestEvent
			if err := json.Unmarshal(got.Data, &event); err != nil || event.PullRequest.Number != 42 {
				t.Fatalf("data = %s, %v", got.Data, err)
			}
		})
	}
}

func TestHandleWebhookEventWithoutDeliveryIDForwardsNothing(t *testing.T) {
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	publisher := &recordingAutomationEvents{}
	svc.AutomationEvents = publisher
	_ = svc.HandleWebhookEvent(context.Background(), "pull_request", gitHubPullRequestDelivery(t, "opened", nil))
	if len(publisher.events) != 0 {
		t.Fatalf("forwarded %d events without a delivery ID", len(publisher.events))
	}
}

func TestTruncateUTF8(t *testing.T) {
	for _, tc := range []struct {
		in   string
		max  int
		want string
	}{
		{"abc", 5, "abc"},
		{"abcdef", 3, "abc"},
		{"aé", 2, "a"},
		{"a\x00b", 5, "ab"},
		{"a\xffb", 5, "ab"},
	} {
		if got := truncateUTF8(tc.in, tc.max); got != tc.want {
			t.Errorf("truncateUTF8(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}
