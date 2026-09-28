package state

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestMemStoreManagedRealtimeHistoryRetainsAndReportsGap(t *testing.T) {
	m, ctx, acct, app := realtimeFixture(t)
	endpoint, err := m.CreateManagedRealtimeEndpointIfUnderQuota(ctx, ManagedRealtimeEndpoint{
		AccountID: acct.ID, AppID: app.ID, Enabled: true,
	}, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("first")
	first, err := m.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", data, false, "key-1")
	if err != nil || first.Sequence != 1 {
		t.Fatalf("first append = (%+v, %v)", first, err)
	}
	data[0] = 'x'
	duplicate, err := m.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("first"), false, "key-1")
	if err != nil || duplicate.Sequence != 1 || string(duplicate.Data) != "first" {
		t.Fatalf("idempotent append = (%+v, %v)", duplicate, err)
	}
	if _, err := m.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("other"), false, "key-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting key = %v, want ErrConflict", err)
	}
	for i := 2; i <= ManagedRealtimeHistoryMaxMessages+1; i++ {
		message, err := m.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("m"), false, "")
		if err != nil || message.Sequence != int64(i) {
			t.Fatalf("append %d = (%+v, %v)", i, message, err)
		}
	}
	gap, err := m.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, "updates", 0, 10)
	if err != nil || !gap.HistoryUnavailable || gap.OldestSequence != 2 || gap.LatestSequence != 1025 || len(gap.Messages) != 0 {
		t.Fatalf("gap = (%+v, %v)", gap, err)
	}
	page, err := m.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, "updates", 1, 2)
	if err != nil || page.HistoryUnavailable || len(page.Messages) != 2 || page.Messages[0].Sequence != 2 || page.Messages[1].Sequence != 3 {
		t.Fatalf("page = (%+v, %v)", page, err)
	}
	page.Messages[0].Data[0] = 'x'
	again, err := m.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, "updates", 1, 1)
	if err != nil || !bytes.Equal(again.Messages[0].Data, []byte("m")) {
		t.Fatalf("read leaked mutable data = (%+v, %v)", again, err)
	}
	if err := m.DeleteManagedRealtimeEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, "updates", 1, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read after endpoint deletion = %v, want ErrNotFound", err)
	}
}

func TestMemStoreManagedRealtimeHistoryExpiresBeforeCleanup(t *testing.T) {
	m, ctx, acct, app := realtimeFixture(t)
	endpoint, err := m.CreateManagedRealtimeEndpointIfUnderQuota(ctx, ManagedRealtimeEndpoint{
		AccountID: acct.ID, AppID: app.ID, Enabled: true,
	}, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("old"), false, "retry")
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.managedRealtimeHistory[managedRealtimeHistoryKey{endpointID: endpoint.ID, channel: "updates"}].messages[0].CreatedAt = time.Now().Add(-ManagedRealtimeHistoryRetention - time.Minute)
	m.mu.Unlock()
	gap, err := m.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, "updates", 0, 10)
	if err != nil || !gap.HistoryUnavailable || gap.OldestSequence != 2 || gap.LatestSequence != 1 {
		t.Fatalf("expired read = (%+v, %v)", gap, err)
	}
	retry, err := m.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("new"), false, "retry")
	if err != nil || retry.Sequence != first.Sequence+1 {
		t.Fatalf("expired key reuse = (%+v, %v)", retry, err)
	}
	m.mu.Lock()
	m.managedRealtimeHistory[managedRealtimeHistoryKey{endpointID: endpoint.ID, channel: "updates"}].messages[0].CreatedAt = time.Now().Add(-ManagedRealtimeHistoryRetention - time.Minute)
	m.mu.Unlock()
	removed, err := m.PruneExpiredManagedRealtimeChannelMessages(ctx, 10)
	if err != nil || removed != 1 {
		t.Fatalf("prune = (%d, %v)", removed, err)
	}
	page, err := m.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, "updates", 2, 10)
	if err != nil || page.HistoryUnavailable || page.OldestSequence != 3 || page.LatestSequence != 2 || len(page.Messages) != 0 {
		t.Fatalf("after prune = (%+v, %v)", page, err)
	}
}

func TestMemStoreManagedRealtimeHistoryExpiryNeverLeavesMiddleGap(t *testing.T) {
	m, ctx, acct, app := realtimeFixture(t)
	endpoint, err := m.CreateManagedRealtimeEndpointIfUnderQuota(ctx, ManagedRealtimeEndpoint{
		AccountID: acct.ID, AppID: app.ID, Enabled: true,
	}, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := m.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("x"), false, ""); err != nil {
			t.Fatal(err)
		}
	}
	m.mu.Lock()
	m.managedRealtimeHistory[managedRealtimeHistoryKey{endpointID: endpoint.ID, channel: "updates"}].messages[1].CreatedAt = time.Now().Add(-ManagedRealtimeHistoryRetention - time.Minute)
	m.mu.Unlock()
	gap, err := m.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, "updates", 0, 10)
	if err != nil || !gap.HistoryUnavailable || gap.OldestSequence != 3 || gap.LatestSequence != 2 {
		t.Fatalf("middle expiry = (%+v, %v)", gap, err)
	}
	removed, err := m.PruneExpiredManagedRealtimeChannelMessages(ctx, 10)
	if err != nil || removed != 2 {
		t.Fatalf("middle prune = (%d, %v)", removed, err)
	}
}

func TestMemStoreManagedRealtimeHistoryChannelLimit(t *testing.T) {
	m, ctx, acct, app := realtimeFixture(t)
	endpoint, err := m.CreateManagedRealtimeEndpointIfUnderQuota(ctx, ManagedRealtimeEndpoint{
		AccountID: acct.ID, AppID: app.ID, Enabled: true,
	}, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	for i := range ManagedRealtimeHistoryMaxChannels {
		if _, err := m.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, fmt.Sprintf("channel-%d", i), []byte("x"), false, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "overflow", []byte("x"), false, ""); !errors.Is(err, ErrManagedRealtimeHistoryLimit) {
		t.Fatalf("overflow = %v, want channel limit", err)
	}
	if _, err := m.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "channel-0", []byte("x"), false, ""); err != nil {
		t.Fatalf("existing channel should still append: %v", err)
	}
}

func TestMemStoreManagedRealtimeHistoryRejectsInvalidRead(t *testing.T) {
	m, ctx, acct, app := realtimeFixture(t)
	endpoint, err := m.CreateManagedRealtimeEndpointIfUnderQuota(ctx, ManagedRealtimeEndpoint{
		AccountID: acct.ID, AppID: app.ID, Enabled: true,
	}, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, "updates", -1, 1); !errors.Is(err, ErrManagedRealtimeHistoryInvalid) {
		t.Fatalf("negative cursor = %v", err)
	}
	if _, err := m.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", make([]byte, ManagedRealtimeHistoryMaxPayloadBytes+1), false, ""); !errors.Is(err, ErrManagedRealtimeHistoryInvalid) {
		t.Fatalf("oversized payload = %v", err)
	}
	if _, err := m.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, "updates", 1, 1); !errors.Is(err, ErrManagedRealtimeHistoryInvalid) {
		t.Fatalf("future cursor = %v", err)
	}
}
