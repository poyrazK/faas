package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

const AnonymousConsumerKey = "__anonymous__"

// ConsumerUsageStore is intentionally narrower than Store. The usage ledger
// is an additive capability used by the apid gRPC receiver and the owner read
// surface; keeping it separate avoids forcing every Store test double to
// implement billing-specific methods during the rollout.
type ConsumerUsageStore interface {
	RecordAPIConsumerUsage(context.Context, APIConsumerUsageEvent) (bool, error)
	ListAPIConsumerUsage(context.Context, string, string, string, time.Time, time.Time) ([]APIConsumerUsageBucket, error)
}

func validateAPIConsumerUsageEvent(event APIConsumerUsageEvent) error {
	if _, err := uuid.Parse(event.EventID); err != nil {
		return fmt.Errorf("consumer usage: event_id must be a UUID: %w", err)
	}
	if _, err := uuid.Parse(event.AccountID); err != nil {
		return fmt.Errorf("consumer usage: account_id must be a UUID: %w", err)
	}
	if _, err := uuid.Parse(event.AppID); err != nil {
		return fmt.Errorf("consumer usage: app_id must be a UUID: %w", err)
	}
	if event.ConsumerKey != AnonymousConsumerKey {
		if _, err := uuid.Parse(event.ConsumerKey); err != nil {
			return fmt.Errorf("consumer usage: consumer_key must be a UUID or %q: %w", AnonymousConsumerKey, err)
		}
	}
	if event.WindowStart.IsZero() {
		return fmt.Errorf("consumer usage: window_start is required")
	}
	if !event.WindowStart.Equal(event.WindowStart.UTC().Truncate(time.Minute)) {
		return fmt.Errorf("consumer usage: window_start must be a UTC minute")
	}
	if event.RequestCount <= 0 {
		return fmt.Errorf("consumer usage: request_count must be positive")
	}
	if event.ErrorCount < 0 || event.ErrorCount > event.RequestCount {
		return fmt.Errorf("consumer usage: error_count must be between zero and request_count")
	}
	if event.BillableUnits < 0 || event.BillableUnits > event.RequestCount {
		return fmt.Errorf("consumer usage: billable_units must be between zero and request_count")
	}
	return nil
}

func consumerUsageBucketKey(accountID, appID, consumerKey string, minute time.Time) string {
	return accountID + "\x00" + appID + "\x00" + consumerKey + "\x00" + minute.UTC().Format(time.RFC3339)
}

// RecordAPIConsumerUsage applies one event exactly once and returns true when
// the event changed the aggregate. The MemStore implementation mirrors the
// Postgres event-ledger transaction and is used by handler tests.
func (m *MemStore) RecordAPIConsumerUsage(_ context.Context, event APIConsumerUsageEvent) (bool, error) {
	if err := validateAPIConsumerUsageEvent(event); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.apiConsumerUsageEvents[event.EventID]; exists {
		return false, nil
	}
	key := consumerUsageBucketKey(event.AccountID, event.AppID, event.ConsumerKey, event.WindowStart)
	bucket := m.apiConsumerUsage[key]
	if bucket.AppID == "" {
		bucket = APIConsumerUsageBucket{
			AccountID: event.AccountID, AppID: event.AppID, ConsumerKey: event.ConsumerKey,
			WindowStart: event.WindowStart.UTC(),
		}
	}
	bucket.RequestCount += event.RequestCount
	bucket.ErrorCount += event.ErrorCount
	bucket.BillableUnits += event.BillableUnits
	m.apiConsumerUsage[key] = bucket
	m.apiConsumerUsageEvents[event.EventID] = struct{}{}
	return true, nil
}

func (m *MemStore) ListAPIConsumerUsage(_ context.Context, accountID, appID, consumerKey string, since, until time.Time) ([]APIConsumerUsageBucket, error) {
	if accountID == "" || appID == "" || consumerKey == "" || !until.After(since) {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []APIConsumerUsageBucket
	for _, bucket := range m.apiConsumerUsage {
		if bucket.AccountID != accountID || bucket.AppID != appID || bucket.ConsumerKey != consumerKey {
			continue
		}
		if bucket.WindowStart.Before(since) || !bucket.WindowStart.Before(until) {
			continue
		}
		out = append(out, bucket)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WindowStart.Before(out[j].WindowStart) })
	return out, nil
}
