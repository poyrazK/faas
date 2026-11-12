// adr: 20261030125501 §4 — gregale_github_subscribe records one
// subscription per tool call, at most the plan's live subscriptions per
// endpoint; a repository's event reaches only the live subscriptions of its
// account to that type, and expired ones are deleted.
package agents_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state/agents"
)

func TestGitHubSubscriptions(t *testing.T) {
	pool := open(t)
	s := agents.New(pool)
	ctx := context.Background()
	const endpoint, otherEndpoint = "44444444-4444-4444-4444-444444444444", "55555555-5555-5555-5555-555555555555"
	for _, q := range []string{
		`INSERT INTO accounts (id) VALUES ('` + account + `'), ('` + other + `') ON CONFLICT DO NOTHING`,
		`INSERT INTO managed_realtime_endpoints VALUES ('` + endpoint + `'), ('` + otherEndpoint + `')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	sub := func(endpointID, thread, call, repository string, types ...string) agents.GitHubSubscription {
		return agents.GitHubSubscription{AccountID: account, EndpointID: endpointID, ThreadID: thread, ToolCallID: call, Repository: repository,
			EventTypes: types, IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
	}
	first, existing, err := s.SubscribeGitHub(ctx, sub(endpoint, "t-1", "call_1", "acme/app", "pull_request.opened", "pull_request.closed"), now, 2)
	if err != nil || existing || first.ID == "" {
		t.Fatalf("subscribe = %+v, %v, %v", first, existing, err)
	}
	// A retried call finds its subscription, even with other arguments.
	again, existing, err := s.SubscribeGitHub(ctx, sub(endpoint, "t-1", "call_1", "acme/lib", "pull_request.opened"), now, 2)
	if err != nil || !existing || again.ID != first.ID || again.Repository != "acme/app" || len(again.EventTypes) != 2 {
		t.Fatalf("a retried call = %+v, %v, %v", again, existing, err)
	}
	if _, _, err := s.SubscribeGitHub(ctx, sub(endpoint, "t-2", "call_2", "acme/app", "pull_request.opened"), now, 2); err != nil {
		t.Fatal(err)
	}
	var limit *agents.LimitError
	if _, _, err := s.SubscribeGitHub(ctx, sub(endpoint, "t-3", "call_3", "acme/app", "pull_request.opened"), now, 2); !errors.As(err, &limit) || limit.Observed != 2 {
		t.Fatalf("a third live subscription at max 2 = %v", err)
	}
	if _, _, err := s.SubscribeGitHub(ctx, sub(otherEndpoint, "t-1", "call_1", "acme/lib", "pull_request.opened"), now, 2); err != nil {
		t.Fatalf("another endpoint's limit is its own: %v", err)
	}

	threads := func(repository, eventType string, at time.Time) []string {
		t.Helper()
		subs, err := s.GitHubSubscribers(ctx, account, repository, eventType, at, 10)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range subs {
			if !s.IssuedAt.Equal(now) || s.EndpointID == "" || s.ToolCallID == "" {
				t.Fatalf("subscriber %+v", s)
			}
			out = append(out, s.EndpointID[:2]+"/"+s.ThreadID)
		}
		return out
	}
	if got := threads("acme/app", "pull_request.opened", now); !slices.Equal(got, []string{"44/t-1", "44/t-2"}) {
		t.Fatalf("opened subscribers %v", got)
	}
	if got := threads("acme/app", "pull_request.closed", now); !slices.Equal(got, []string{"44/t-1"}) {
		t.Fatalf("closed subscribers %v", got)
	}
	if got := threads("acme/lib", "pull_request.opened", now); !slices.Equal(got, []string{"55/t-1"}) {
		t.Fatalf("another repository's subscribers %v", got)
	}
	if subs, _ := s.GitHubSubscribers(ctx, other, "acme/app", "pull_request.opened", now, 10); len(subs) != 0 {
		t.Fatalf("another account's event reached %+v", subs)
	}
	later := now.Add(2 * time.Hour)
	if got := threads("acme/app", "pull_request.opened", later); len(got) != 0 {
		t.Fatalf("expired subscriptions still receive: %v", got)
	}
	if _, _, err := s.SubscribeGitHub(ctx, sub(endpoint, "t-3", "call_3", "acme/app", "pull_request.opened"), later, 2); err == nil {
		t.Fatal("a subscription that expires before now was recorded")
	}
	expiring := sub(endpoint, "t-3", "call_3", "acme/app", "pull_request.opened")
	expiring.ExpiresAt = later.Add(time.Hour)
	if _, _, err := s.SubscribeGitHub(ctx, expiring, later, 2); err != nil {
		t.Fatalf("expired subscriptions still count against the limit: %v", err)
	}
	if n, err := s.DeleteExpiredGitHubSubscriptions(ctx, later, 10); err != nil || n != 3 {
		t.Fatalf("deleted %d expired, %v; want 3", n, err)
	}
}
