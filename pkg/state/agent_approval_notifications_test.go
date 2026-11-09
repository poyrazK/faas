package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261107140000 — a new request with a review page gets one
// notification row; meterd claims it under a lease, can defer, retry or
// finish it, and keeps when it last emailed each recipient about a key.
func exerciseAgentApprovalNotifications(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().Format("150405.000000")
	acct, err := store.CreateAccount(ctx, "approval-notify-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	_, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(ctx, acct.ID, hash, "notify-"+suffix, []string{api.ScopeAgent})
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	request := func(path, reviewURL string) state.AgentApproval {
		t.Helper()
		a, err := store.RequestAgentApproval(ctx, state.AgentApproval{
			ID: uuid.NewString(), KeyID: key.ID, AccountID: acct.ID, OrgID: key.OrgID, Category: api.AgentApprovalDelete,
			Method: "DELETE", Path: path, ExpiresAt: time.Now().Add(api.AgentApprovalTTL),
			ReviewURL: reviewURL, AgentName: "notify-agent",
		})
		if err != nil {
			t.Fatalf("RequestAgentApproval %s: %v", path, err)
		}
		return a
	}
	claimOwn := func() []state.AgentApprovalNotification {
		t.Helper()
		claimed, err := store.ClaimAgentApprovalNotifications(ctx, 1000, time.Minute)
		if err != nil {
			t.Fatalf("ClaimAgentApprovalNotifications: %v", err)
		}
		var own []state.AgentApprovalNotification
		for _, n := range claimed {
			if n.KeyID == key.ID {
				own = append(own, n)
			}
		}
		return own
	}

	request("/v1/apps/no-review-page", "")
	first := request("/v1/apps/one", "https://example.com/review/1")
	claimed := claimOwn()
	if len(claimed) != 1 || claimed[0].ApprovalID != first.ID || claimed[0].AgentName != "notify-agent" ||
		claimed[0].ReviewURL != "https://example.com/review/1" || claimed[0].AccountID != acct.ID {
		t.Fatalf("claimed = %+v; want only the request with a review page", claimed)
	}
	if again := claimOwn(); len(again) != 0 {
		t.Fatalf("a leased notification was claimed twice: %+v", again)
	}
	lines, err := store.PendingAgentApprovalNotificationsForKey(ctx, key.ID)
	if err != nil || len(lines) != 1 || lines[0].ApprovalID != first.ID || lines[0].Path != "/v1/apps/one" {
		t.Fatalf("pending lines = %+v, %v", lines, err)
	}
	if status, expires, err := store.AgentApprovalRequestState(ctx, first.ID); err != nil || status != api.AgentApprovalPending || !expires.After(time.Now()) {
		t.Fatalf("request state = %q %v %v", status, expires, err)
	}

	if err := store.DeferAgentApprovalNotification(ctx, first.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("defer: %v", err)
	}
	if again := claimOwn(); len(again) != 0 {
		t.Fatalf("a deferred notification came due early: %+v", again)
	}
	if err := store.DeferAgentApprovalNotification(ctx, first.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("defer to now: %v", err)
	}
	if err := store.RetryAgentApprovalNotification(ctx, first.ID, "smtp down", 0, 2); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retried := claimOwn(); len(retried) != 1 || retried[0].Attempts != 1 {
		t.Fatalf("after one failure = %+v; want it due with one attempt", retried)
	}
	if err := store.RetryAgentApprovalNotification(ctx, first.ID, "smtp still down", 0, 2); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if failed := claimOwn(); len(failed) != 0 {
		t.Fatalf("a notification past its last attempt is still due: %+v", failed)
	}

	second := request("/v1/apps/two", "https://example.com/review/2")
	if err := store.FinishAgentApprovalNotifications(ctx, []string{second.ID}, state.AgentApprovalNotificationSent, ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if done := claimOwn(); len(done) != 0 {
		t.Fatalf("a sent notification is still due: %+v", done)
	}

	if _, ok, err := store.AgentApprovalEmailLastSent(ctx, acct.ID, key.ID); err != nil || ok {
		t.Fatalf("email log before any email = %v, %v", ok, err)
	}
	if err := store.RecordAgentApprovalEmailSent(ctx, acct.ID, key.ID); err != nil {
		t.Fatalf("record: %v", err)
	}
	if at, ok, err := store.AgentApprovalEmailLastSent(ctx, acct.ID, key.ID); err != nil || !ok || time.Since(at) > time.Minute {
		t.Fatalf("email log = %v, %v, %v", at, ok, err)
	}

	// §3: channels bound for the account's keys, and for the organization by
	// another member, both reach a request; another account's channel can't
	// be bound.
	channel := func(owner state.Account, name string) state.NotificationChannel {
		t.Helper()
		ch, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{
			AccountID: owner.ID, Name: name, Kind: "email", Email: name + "@example.com",
		}, 10)
		if err != nil {
			t.Fatalf("CreateNotificationChannel %s: %v", name, err)
		}
		return ch
	}
	other, err := store.CreateAccount(ctx, "approval-notify-admin-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	own, orgChannel, foreign := channel(acct, "own-ops"), channel(other, "org-ops"), channel(other, "not-yours")
	if err := store.SetAgentApprovalChannels(ctx, acct.ID, "", []string{own.ID, foreign.ID}); err != nil {
		t.Fatalf("SetAgentApprovalChannels(account): %v", err)
	}
	if bound, err := store.AgentApprovalChannels(ctx, acct.ID, ""); err != nil || len(bound) != 1 || bound[0].ID != own.ID {
		t.Fatalf("account bindings = %+v, %v; want only the account's own channel", bound, err)
	}
	orgID := uuid.NewString()
	if err := store.SetAgentApprovalChannels(ctx, other.ID, orgID, []string{orgChannel.ID}); err != nil {
		t.Fatalf("SetAgentApprovalChannels(org): %v", err)
	}
	reached, err := store.NotificationChannelsForAgentApproval(ctx, acct.ID, orgID)
	if err != nil || len(reached) != 2 {
		t.Fatalf("channels for a request in the org = %+v, %v; want the account's and the org's", reached, err)
	}
	if reached, err := store.NotificationChannelsForAgentApproval(ctx, acct.ID, uuid.NewString()); err != nil || len(reached) != 1 || reached[0].ID != own.ID {
		t.Fatalf("channels for a request in another org = %+v, %v; want the account's only", reached, err)
	}
	if err := store.SetAgentApprovalChannels(ctx, acct.ID, "", nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if bound, err := store.AgentApprovalChannels(ctx, acct.ID, ""); err != nil || len(bound) != 0 {
		t.Fatalf("cleared bindings = %+v, %v", bound, err)
	}

	third := request("/v1/apps/three", "https://example.com/review/3")
	if err := store.MarkAgentApprovalChannelsNotified(ctx, third.ID); err != nil {
		t.Fatalf("MarkAgentApprovalChannelsNotified: %v", err)
	}
	if claimed := claimOwn(); len(claimed) != 1 || claimed[0].ApprovalID != third.ID || !claimed[0].ChannelsNotified {
		t.Fatalf("claimed = %+v; want the third request marked as sent to its channels", claimed)
	}
}

func TestMemStoreAgentApprovalNotifications(t *testing.T) {
	exerciseAgentApprovalNotifications(t, state.NewMemStore())
}

func TestPgStoreAgentApprovalNotifications(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAgentApprovalNotifications(t, state.NewPgStore(pool))
}
