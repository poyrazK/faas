package state_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1323 (amendment of 2026-10-31) — the inbox cap counts inboxes that hold
// messages now, so an inbox whose messages expired frees its slot.
func TestPgStoreManagedRealtimeExpiredInboxFreesItsSlot(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-realtime-inbox-freed", "realtime-inbox-freed")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	for i := range state.ManagedRealtimeInboxMaxPrincipals {
		if _, err := s.AppendManagedRealtimeInboxMessage(ctx, endpoint.ID, fmt.Sprintf("user-%d", i), []byte("x"), false, fmt.Sprintf("m-%d", i)); err != nil {
			t.Fatalf("inbox %d: %v", i, err)
		}
	}
	if _, err := s.AppendManagedRealtimeInboxMessage(ctx, endpoint.ID, "next", []byte("x"), false, "m-next"); !errors.Is(err, state.ErrManagedRealtimeHistoryLimit) {
		t.Fatalf("inbox past the cap = %v, want the history limit", err)
	}
	if _, err := pool.Exec(ctx, `update managed_realtime_inbox_messages set created_at = $2 where endpoint_id = $1
 and channel = (select channel from managed_realtime_inbox_heads where endpoint_id = $1 order by channel limit 1)`,
		endpoint.ID, time.Now().Add(-state.ManagedRealtimeInboxRetention-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if removed, err := s.PruneExpiredManagedRealtimeInboxMessages(ctx, 10); err != nil || removed != 1 {
		t.Fatalf("prune = (%d, %v)", removed, err)
	}
	if _, err := s.AppendManagedRealtimeInboxMessage(ctx, endpoint.ID, "next", []byte("x"), false, "m-next"); err != nil {
		t.Fatalf("inbox after an expired one = %v, want admitted", err)
	}
}

// adr: 1323 (amendment of 2026-10-31) — read markers count for 30 days after
// their last update and are pruned after that.
func TestPgStoreManagedRealtimeReadMarkersExpire(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-realtime-markers", "realtime-markers")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "orders", []byte("x"), false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into managed_realtime_read_progress(endpoint_id,principal,stream,inbox,sequence)
 select $1, encode(sha256(('marker-'||g)::bytea),'hex'), 'orders', false, 1 from generate_series(1, $2::integer) g`,
		endpoint.ID, state.ManagedRealtimeMaxReadMarkers); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdvanceManagedRealtimeReadProgress(ctx, endpoint.ID, "bob", "orders", false, 1); !errors.Is(err, state.ErrManagedRealtimeDurableCursorLimit) {
		t.Fatalf("marker past the cap = %v, want the limit", err)
	}
	if _, err := pool.Exec(ctx, `update managed_realtime_read_progress set updated_at = $2 where endpoint_id = $1`,
		endpoint.ID, time.Now().Add(-state.ManagedRealtimeReadMarkerRetention-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdvanceManagedRealtimeReadProgress(ctx, endpoint.ID, "bob", "orders", false, 1); err != nil {
		t.Fatalf("marker beside expired ones = %v, want admitted", err)
	}
	if removed, err := s.PruneExpiredManagedRealtimeReadMarkers(ctx, 1000); err != nil || removed != 1000 {
		t.Fatalf("prune = (%d, %v), want a full batch of 1000", removed, err)
	}
}

// adr: 1323 (amendment of 2026-10-31) — notification preferences are capped
// per account by plan, and resetting a user to the defaults frees the slot.
func TestPgStoreManagedRealtimeNotificationPreferencesPerAccount(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-realtime-prefs", "realtime-prefs")
	if _, err := pool.Exec(ctx, `update accounts set plan = $2 where id = $1`, accountID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	limits, _ := api.RealtimeLimitsFor(api.PlanHobby)
	first, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	quiet := api.RealtimeNotificationPreferences{Enabled: false, Categories: map[string]bool{}}
	if err := s.PutManagedRealtimeNotificationPreferences(ctx, first.ID, "alice", quiet); err != nil {
		t.Fatal(err)
	}
	// The rest of the account's allowance sits on the other endpoint.
	if _, err := pool.Exec(ctx, `insert into managed_realtime_push_preferences(endpoint_id,principal,preferences)
 select $1, encode(sha256(('prefs-'||g)::bytea),'hex'), '{"enabled":false,"categories":{}}' from generate_series(1, $2::integer) g`,
		second.ID, limits.NotificationPreferencesPerAccount-1); err != nil {
		t.Fatal(err)
	}
	if err := s.PutManagedRealtimeNotificationPreferences(ctx, first.ID, "bob", quiet); !errors.Is(err, state.ErrManagedRealtimeDurableCursorLimit) {
		t.Fatalf("user past the account cap = %v, want the limit", err)
	}
	if err := s.PutManagedRealtimeNotificationPreferences(ctx, first.ID, "alice", api.DefaultRealtimeNotificationPreferences()); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetManagedRealtimeNotificationPreferences(ctx, first.ID, "alice"); err != nil || !got.Enabled {
		t.Fatalf("alice after a reset = %+v, %v; want the defaults", got, err)
	}
	if err := s.PutManagedRealtimeNotificationPreferences(ctx, first.ID, "bob", quiet); err != nil {
		t.Fatalf("user after another's reset = %v, want admitted", err)
	}
}
