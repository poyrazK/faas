package state_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreManagedRealtimeHistorySerializesPublishers(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-realtime-history", "realtime-history")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	const publishers = 24
	var wg sync.WaitGroup
	results := make(chan int64, publishers)
	errorsCh := make(chan error, publishers)
	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			message, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("hello"), false, "")
			if err != nil {
				errorsCh <- err
				return
			}
			results <- message.Sequence
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Errorf("append: %v", err)
	}
	if t.Failed() {
		return
	}
	close(results)
	seen := make(map[int64]bool)
	for sequence := range results {
		seen[sequence] = true
	}
	if len(seen) != publishers {
		t.Fatalf("sequences = %v, want %d distinct", seen, publishers)
	}
	for sequence := int64(1); sequence <= publishers; sequence++ {
		if !seen[sequence] {
			t.Fatalf("sequence %d missing from %v", sequence, seen)
		}
	}
	first, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("stable"), false, "stable-key")
	if err != nil || first.Sequence != publishers+1 {
		t.Fatalf("keyed append = (%+v, %v)", first, err)
	}
	duplicate, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("stable"), false, "stable-key")
	if err != nil || duplicate.Sequence != first.Sequence {
		t.Fatalf("duplicate = (%+v, %v)", duplicate, err)
	}
	if _, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("different"), false, "stable-key"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("conflicting key = %v", err)
	}
	page, err := s.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, "updates", publishers-1, 2)
	if err != nil || len(page.Messages) != 2 || page.Messages[0].Sequence != publishers || page.Messages[1].Sequence != publishers+1 {
		t.Fatalf("page = (%+v, %v)", page, err)
	}
}

func TestPgStoreManagedRealtimeHistoryStorageObservation(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-realtime-storage", "realtime-storage")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("payload"), false, ""); err != nil {
		t.Fatal(err)
	}
	stats, err := s.ObserveManagedRealtimeHistoryStorage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.HeadsRelationBytes <= 0 || stats.MessagesRelationBytes <= 0 || stats.UsageRelationBytes <= 0 {
		t.Fatalf("physical history allocation = %+v, want both relations allocated", stats)
	}
}

func TestPgStoreManagedRealtimeHistoryUsageScopesAccountAndReplayFloor(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-realtime-usage-a", "realtime-usage-a")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"a", "bb", "ccc"} {
		if _, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte(data), false, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "empty-later", []byte("x"), false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update managed_realtime_channel_messages set created_at = $4 where endpoint_id = $1 and channel = $2 and sequence = $3`,
		endpoint.ID, "updates", 2, time.Now().Add(-state.ManagedRealtimeHistoryRetention-time.Minute)); err != nil {
		t.Fatal(err)
	}
	otherAccountID, otherAppID, _ := seedLiveDeploy(t, s, ctx, "-realtime-usage-b", "realtime-usage-b")
	otherEndpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(otherAccountID, otherAppID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendManagedRealtimeChannelMessage(ctx, otherEndpoint.ID, "private", []byte("secret"), false, ""); err != nil {
		t.Fatal(err)
	}
	usage, err := s.ReadManagedRealtimeHistoryUsage(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.EndpointCount != 1 || usage.ChannelCount != 2 || usage.StoredMessageCount != 4 || usage.StoredPayloadBytes != 7 ||
		usage.ReplayableMessageCount != 2 || usage.ReplayablePayloadBytes != 4 || usage.ObservedAt.IsZero() {
		t.Fatalf("account usage = %+v", usage)
	}
	other, err := s.ReadManagedRealtimeHistoryUsage(ctx, otherAccountID)
	if err != nil || other.EndpointCount != 1 || other.ChannelCount != 1 || other.StoredMessageCount != 1 || other.StoredPayloadBytes != 6 {
		t.Fatalf("other account usage = (%+v, %v)", other, err)
	}
	if err := s.DeleteManagedRealtimeEndpoint(ctx, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	usage, err = s.ReadManagedRealtimeHistoryUsage(ctx, accountID)
	if err != nil || usage.EndpointCount != 0 || usage.ChannelCount != 0 || usage.StoredMessageCount != 0 {
		t.Fatalf("deleted account usage = (%+v, %v)", usage, err)
	}
}

func TestPgStoreManagedRealtimeHistoryExpiryAndKeyReuse(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-realtime-expiry", "realtime-expiry")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("old"), false, "retry")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update managed_realtime_channel_messages set created_at = $4 where endpoint_id = $1 and channel = $2 and sequence = $3`,
		endpoint.ID, "updates", first.Sequence, time.Now().Add(-state.ManagedRealtimeHistoryRetention-time.Minute)); err != nil {
		t.Fatal(err)
	}
	gap, err := s.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, "updates", 0, 10)
	if err != nil || !gap.HistoryUnavailable || gap.OldestSequence != 2 || gap.LatestSequence != 1 {
		t.Fatalf("expired read = (%+v, %v)", gap, err)
	}
	retry, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("new"), false, "retry")
	if err != nil || retry.Sequence != 2 {
		t.Fatalf("expired key reuse = (%+v, %v)", retry, err)
	}
	if _, err := pool.Exec(ctx, `update managed_realtime_channel_messages set created_at = $4 where endpoint_id = $1 and channel = $2 and sequence = $3`,
		endpoint.ID, "updates", retry.Sequence, time.Now().Add(-state.ManagedRealtimeHistoryRetention-time.Minute)); err != nil {
		t.Fatal(err)
	}
	removed, err := s.PruneExpiredManagedRealtimeChannelMessages(ctx, 10)
	if err != nil || removed != 1 {
		t.Fatalf("prune = (%d, %v)", removed, err)
	}
	page, err := s.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, "updates", 2, 10)
	if err != nil || page.HistoryUnavailable || page.OldestSequence != 3 || page.LatestSequence != 2 || len(page.Messages) != 0 {
		t.Fatalf("after prune = (%+v, %v)", page, err)
	}
}

func TestPgStoreManagedRealtimeHistoryExpiryNeverLeavesMiddleGap(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-realtime-middle", "realtime-middle")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("x"), false, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `update managed_realtime_channel_messages set created_at = $4 where endpoint_id = $1 and channel = $2 and sequence = $3`,
		endpoint.ID, "updates", 2, time.Now().Add(-state.ManagedRealtimeHistoryRetention-time.Minute)); err != nil {
		t.Fatal(err)
	}
	gap, err := s.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, "updates", 0, 10)
	if err != nil || !gap.HistoryUnavailable || gap.OldestSequence != 3 || gap.LatestSequence != 2 {
		t.Fatalf("middle expiry = (%+v, %v)", gap, err)
	}
	removed, err := s.PruneExpiredManagedRealtimeChannelMessages(ctx, 10)
	if err != nil || removed != 2 {
		t.Fatalf("middle prune = (%d, %v)", removed, err)
	}
}

func TestPgStoreManagedRealtimeHistoryChannelLimit(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-realtime-channels", "realtime-channels")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	for i := range state.ManagedRealtimeHistoryMaxChannels {
		if _, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, fmt.Sprintf("channel-%d", i), []byte("x"), false, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "overflow", []byte("x"), false, ""); !errors.Is(err, state.ErrManagedRealtimeHistoryLimit) {
		t.Fatalf("overflow = %v, want channel limit", err)
	}
	if _, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "channel-0", []byte("x"), false, ""); err != nil {
		t.Fatalf("existing channel should still append: %v", err)
	}
}

func TestPgStoreManagedRealtimeHistoryTrimMakesGapExplicit(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-realtime-trim", "realtime-trim")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into managed_realtime_channel_heads(endpoint_id, channel, next_sequence, oldest_sequence)
		values ($1, 'updates', 1025, 1)
	`, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into managed_realtime_channel_messages(endpoint_id, channel, sequence, data)
		select $1, 'updates', series, 'x'::bytea
		from generate_series(1, 1024) as series
	`, endpoint.ID); err != nil {
		t.Fatal(err)
	}
	message, err := s.AppendManagedRealtimeChannelMessage(ctx, endpoint.ID, "updates", []byte("new"), false, "")
	if err != nil || message.Sequence != 1025 {
		t.Fatalf("append = (%+v, %v)", message, err)
	}
	gap, err := s.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, "updates", 0, 10)
	if err != nil || !gap.HistoryUnavailable || gap.OldestSequence != 2 || gap.LatestSequence != 1025 {
		t.Fatalf("gap = (%+v, %v)", gap, err)
	}
	page, err := s.ReadManagedRealtimeChannelHistory(ctx, endpoint.ID, "updates", 1024, 1)
	if err != nil || page.HistoryUnavailable || len(page.Messages) != 1 || page.Messages[0].Sequence != 1025 {
		t.Fatalf("page = (%+v, %v)", page, err)
	}
}
