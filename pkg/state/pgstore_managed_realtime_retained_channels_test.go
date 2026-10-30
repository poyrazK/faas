package state_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1323 — retained channel limits count channels that hold messages now,
// so a channel whose messages expired and were pruned frees its slot.
func TestPgStoreManagedRealtimeExpiredChannelFreesItsSlot(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-realtime-freed", "realtime-freed")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	for i := range state.ManagedRealtimeHistoryMaxChannels {
		if _, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, fmt.Sprintf("channel-%d", i), []byte("x"), false, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "next", []byte("x"), false, ""); !errors.Is(err, state.ErrManagedRealtimeHistoryLimit) {
		t.Fatalf("33rd channel = %v, want channel limit", err)
	}
	if _, err := pool.Exec(ctx, `update managed_realtime_channel_messages set created_at = $3 where endpoint_id = $1 and channel = $2`,
		endpoint.ID, "channel-0", time.Now().Add(-state.ManagedRealtimeHistoryRetention-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if removed, err := s.PruneExpiredManagedRealtimeChannelMessages(ctx, 10); err != nil || removed != 1 {
		t.Fatalf("prune = (%d, %v)", removed, err)
	}
	if _, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "next", []byte("x"), false, ""); err != nil {
		t.Fatalf("channel after an expired one = %v, want admitted", err)
	}
	// The dormant channel keeps its sequence: retaining on it again needs a
	// slot, and continues where it left off.
	if _, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "channel-0", []byte("x"), false, ""); !errors.Is(err, state.ErrManagedRealtimeHistoryLimit) {
		t.Fatalf("dormant channel back at the limit = %v, want channel limit", err)
	}
}

// adr: 1323 — an account holds its plan's retained channels across all of its
// endpoints, below each endpoint's own 32.
func TestPgStoreManagedRealtimeRetainedChannelsPerAccount(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-realtime-account", "realtime-account")
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
	onFirst := limits.RetainedChannelsPerAccount / 2
	for i := range limits.RetainedChannelsPerAccount {
		endpoint := first.ID
		if i >= onFirst {
			endpoint = second.ID
		}
		if _, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint, fmt.Sprintf("channel-%d", i), []byte("x"), false, ""); err != nil {
			t.Fatalf("channel %d: %v", i, err)
		}
	}
	_, err = s.AppendManagedRealtimeChannelMessage(ctx, second.ID, "one-more", []byte("x"), false, "")
	var limit *state.ManagedRealtimeRetainedChannelLimitError
	if !errors.As(err, &limit) || limit.Limit != limits.RetainedChannelsPerAccount || limit.Observed != limits.RetainedChannelsPerAccount {
		t.Fatalf("channel past the account allowance = %v, want the account limit", err)
	}
	if !errors.Is(err, state.ErrManagedRealtimeHistoryLimit) {
		t.Fatalf("account limit %v does not unwrap to the history limit", err)
	}
	if _, err := s.AppendManagedRealtimeChannelMessage(ctx, first.ID, "channel-0", []byte("x"), false, ""); err != nil {
		t.Fatalf("a held channel still appends: %v", err)
	}
}
