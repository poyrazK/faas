package state

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1323 — the MemStore counts held channels like the PgStore: an expired
// and pruned channel frees its slot, and an account holds its plan's share.
func TestMemStoreManagedRealtimeRetainedChannels(t *testing.T) {
	m, ctx, acct, app := realtimeFixture(t)
	m.mu.Lock()
	account := m.accounts[acct.ID]
	account.Plan = api.PlanHobby
	m.accounts[acct.ID] = account
	m.mu.Unlock()
	limits, _ := api.RealtimeLimitsFor(api.PlanHobby)
	var endpoints []string
	for range 2 {
		endpoint, err := m.CreateManagedRealtimeEndpointIfUnderQuota(ctx, ManagedRealtimeEndpoint{AccountID: acct.ID, AppID: app.ID, Enabled: true}, 2, 10)
		if err != nil {
			t.Fatal(err)
		}
		endpoints = append(endpoints, endpoint.ID)
	}
	for i := range limits.RetainedChannelsPerAccount {
		if _, err := m.AppendManagedRealtimeChannelMessage(ctx, endpoints[i%2], fmt.Sprintf("channel-%d", i), []byte("x"), false, ""); err != nil {
			t.Fatalf("channel %d: %v", i, err)
		}
	}
	_, err := m.AppendManagedRealtimeChannelMessage(ctx, endpoints[1], "one-more", []byte("x"), false, "")
	var limit *ManagedRealtimeRetainedChannelLimitError
	if !errors.As(err, &limit) || limit.Limit != limits.RetainedChannelsPerAccount {
		t.Fatalf("channel past the account allowance = %v", err)
	}
	m.mu.Lock()
	history := m.managedRealtimeHistory[managedRealtimeHistoryKey{endpointID: endpoints[0], channel: "channel-0"}]
	for i := range history.messages {
		history.messages[i].CreatedAt = time.Now().Add(-ManagedRealtimeHistoryRetention - time.Minute)
	}
	m.mu.Unlock()
	if _, err := m.PruneExpiredManagedRealtimeChannelMessages(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AppendManagedRealtimeChannelMessage(ctx, endpoints[1], "one-more", []byte("x"), false, ""); err != nil {
		t.Fatalf("channel after an expired one = %v, want admitted", err)
	}
}
