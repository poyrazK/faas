// adr: 20261027090010
package agents_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	gitHubEventAccount  = "4f9c0f6e-8a51-4d4b-9a54-5d4b1c2f9e10"
	gitHubEventDelivery = "72d3162e-cc78-11e3-81ab-4c9367dc0958"
)

type fakeGitHubEventStore struct {
	installations map[int64]string
	accounts      map[string]state.Account
	accepted      map[string][]byte
	acceptErr     error
	calls         int
}

func (f *fakeGitHubEventStore) AccountIDByGitHubInstallation(_ context.Context, id int64) (string, error) {
	if account, ok := f.installations[id]; ok {
		return account, nil
	}
	return "", state.ErrNotFound
}

func (f *fakeGitHubEventStore) AccountByID(_ context.Context, id string) (state.Account, error) {
	if acct, ok := f.accounts[id]; ok {
		return acct, nil
	}
	return state.Account{}, state.ErrNotFound
}

func (f *fakeGitHubEventStore) AcceptPublishedEvent(_ context.Context, actor, accountID string, payload []byte) (state.PublishedEventAcceptance, error) {
	f.calls++
	if f.acceptErr != nil {
		return state.PublishedEventAcceptance{}, f.acceptErr
	}
	if actor != "githubd" || accountID != gitHubEventAccount {
		return state.PublishedEventAcceptance{}, errors.New("unexpected publisher")
	}
	var envelope eventcontract.Envelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return state.PublishedEventAcceptance{}, err
	}
	if previous, ok := f.accepted[envelope.ID]; ok {
		var prior eventcontract.Envelope
		_ = json.Unmarshal(previous, &prior)
		if string(prior.Data) != string(envelope.Data) {
			return state.PublishedEventAcceptance{}, state.ErrConflict
		}
		return state.PublishedEventAcceptance{Duplicate: true}, nil
	}
	f.accepted[envelope.ID] = payload
	return state.PublishedEventAcceptance{}, nil
}

func newFakeGitHubEventStore(status state.AccountStatus) *fakeGitHubEventStore {
	return &fakeGitHubEventStore{installations: map[int64]string{7: gitHubEventAccount},
		accounts: map[string]state.Account{gitHubEventAccount: {ID: gitHubEventAccount, Status: status, Plan: api.PlanPro}},
		accepted: map[string][]byte{}}
}

func gitHubEventData(t *testing.T, edit func(*api.GitHubPullRequestEvent)) []byte {
	t.Helper()
	event := api.GitHubPullRequestEvent{Action: "opened", InstallationID: 7,
		Repository: api.GitHubEventRepository{FullName: "octo/api"},
		PullRequest: api.GitHubEventPullRequest{Number: 42, Title: "Fix", State: "open",
			Head: api.GitHubEventRef{Ref: "fix", SHA: strings.Repeat("a", 40), Repository: "octo/api"},
			Base: api.GitHubEventRef{Ref: "main", SHA: strings.Repeat("b", 40)}}}
	if edit != nil {
		edit(&event)
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestGitHubEventsPublish(t *testing.T) {
	store := newFakeGitHubEventStore(state.AccountActive)
	notified := 0
	events := agents.GitHubEvents{Store: store, Allowed: func(state.Account) bool { return true },
		Notify: func(context.Context) { notified++ }, Now: func() time.Time { return time.Unix(1_800_000_000, 0) }}
	ctx := context.Background()

	result, err := events.Publish(ctx, gitHubEventDelivery, 7, "pull_request.opened", gitHubEventData(t, nil))
	if err != nil || !result.Published || result.Duplicate || result.AccountID != gitHubEventAccount || notified != 1 {
		t.Fatalf("Publish() = %+v, %v; notified %d", result, err, notified)
	}
	var envelope eventcontract.Envelope
	if err := json.Unmarshal(store.accepted[gitHubEventDelivery], &envelope); err != nil {
		t.Fatal(err)
	}
	var data api.GitHubPullRequestEvent
	if envelope.Source != api.GitHubEventSource || envelope.Type != "pull_request.opened" || envelope.AccountID != gitHubEventAccount ||
		json.Unmarshal(envelope.Data, &data) != nil || data.PullRequest.Number != 42 {
		t.Fatalf("envelope = %+v", envelope)
	}

	result, err = events.Publish(ctx, gitHubEventDelivery, 7, "pull_request.opened", gitHubEventData(t, nil))
	if err != nil || !result.Duplicate || notified != 1 {
		t.Fatalf("repeat Publish() = %+v, %v; notified %d", result, err, notified)
	}
	_, err = events.Publish(ctx, gitHubEventDelivery, 7, "pull_request.opened", gitHubEventData(t, func(e *api.GitHubPullRequestEvent) {
		e.PullRequest.Title = "Other"
	}))
	if !errors.Is(err, agents.ErrGitHubEventConflict) {
		t.Fatalf("conflicting Publish() = %v, want ErrGitHubEventConflict", err)
	}
}

func TestGitHubEventsPublishSkips(t *testing.T) {
	cases := []struct {
		name         string
		installation int64
		status       state.AccountStatus
		allowed      func(state.Account) bool
		want         string
	}{
		{"no account", 8, state.AccountActive, func(state.Account) bool { return true }, agents.GitHubEventSkippedNoAccount},
		{"outside rollout", 7, state.AccountActive, func(state.Account) bool { return false }, agents.GitHubEventSkippedNotEnabled},
		{"no rollout", 7, state.AccountActive, nil, agents.GitHubEventSkippedNotEnabled},
		{"suspended", 7, state.AccountSuspended, func(state.Account) bool { return true }, agents.GitHubEventSkippedNotEnabled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeGitHubEventStore(tc.status)
			events := agents.GitHubEvents{Store: store, Allowed: tc.allowed}
			data := gitHubEventData(t, func(e *api.GitHubPullRequestEvent) { e.InstallationID = tc.installation })
			result, err := events.Publish(context.Background(), gitHubEventDelivery, tc.installation, "pull_request.opened", data)
			if err != nil || result.Published || result.Skipped != tc.want || store.calls != 0 {
				t.Fatalf("Publish() = %+v, %v; %d accepts", result, err, store.calls)
			}
		})
	}
}

func TestGitHubEventsPublishRefusesMalformed(t *testing.T) {
	valid := func(t *testing.T) []byte { return gitHubEventData(t, nil) }
	cases := []struct {
		name         string
		delivery     string
		installation int64
		eventType    string
		data         func(*testing.T) []byte
	}{
		{"upper delivery", strings.ToUpper(gitHubEventDelivery), 7, "pull_request.opened", valid},
		{"delivery not a GUID", "delivery-1", 7, "pull_request.opened", valid},
		{"type mismatch", gitHubEventDelivery, 7, "pull_request.closed", valid},
		{"installation mismatch", gitHubEventDelivery, 9, "pull_request.opened", valid},
		{"unknown field", gitHubEventDelivery, 7, "pull_request.opened", func(*testing.T) []byte {
			return []byte(`{"action":"opened","installation_id":7,"body":"x"}`)
		}},
		{"invalid event", gitHubEventDelivery, 7, "pull_request.opened", func(t *testing.T) []byte {
			return gitHubEventData(t, func(e *api.GitHubPullRequestEvent) { e.PullRequest.Head.SHA = "abc" })
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeGitHubEventStore(state.AccountActive)
			events := agents.GitHubEvents{Store: store, Allowed: func(state.Account) bool { return true }}
			_, err := events.Publish(context.Background(), tc.delivery, tc.installation, tc.eventType, tc.data(t))
			if !errors.Is(err, api.ErrGitHubEventInvalid) || store.calls != 0 {
				t.Fatalf("Publish() = %v with %d accepts, want ErrGitHubEventInvalid", err, store.calls)
			}
		})
	}
}

func TestGitHubEventsPublishStoreFailure(t *testing.T) {
	store := newFakeGitHubEventStore(state.AccountActive)
	store.acceptErr = errors.New("database down")
	events := agents.GitHubEvents{Store: store, Allowed: func(state.Account) bool { return true }}
	_, err := events.Publish(context.Background(), gitHubEventDelivery, 7, "pull_request.opened", gitHubEventData(t, nil))
	if err == nil || errors.Is(err, api.ErrGitHubEventInvalid) || errors.Is(err, agents.ErrGitHubEventConflict) {
		t.Fatalf("Publish() = %v, want a retryable error", err)
	}
}
