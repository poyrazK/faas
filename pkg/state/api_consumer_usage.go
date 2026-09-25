package state

import (
	"context"
	"fmt"
	"net/netip"
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

// ValidateAPIConsumerUsageEvent checks the wire-independent financial fact.
// The apid receiver uses it to reject malformed replay records permanently,
// while storage failures remain retryable.
func ValidateAPIConsumerUsageEvent(event APIConsumerUsageEvent) error {
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
	if event.PlatformTenantID != "" {
		if event.ConsumerKey == AnonymousConsumerKey {
			return fmt.Errorf("consumer usage: anonymous traffic cannot have platform_tenant_id")
		}
		if _, err := uuid.Parse(event.PlatformTenantID); err != nil {
			return fmt.Errorf("consumer usage: platform_tenant_id must be a UUID: %w", err)
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
	if audit := event.Audit; audit != nil {
		if event.RequestCount != 1 {
			return fmt.Errorf("request audit: one event must describe exactly one request")
		}
		if audit.RouteTemplate == "" || len(audit.RouteTemplate) > 256 || audit.Method == "" || len(audit.Method) > 16 || audit.HTTPStatus < 100 || audit.HTTPStatus > 599 {
			return fmt.Errorf("request audit: invalid route, method, or status")
		}
		if audit.LatencyMS < 0 || audit.LatencyMS > 86_400_000 || audit.OccurredAt.IsZero() || audit.OccurredAt.After(time.Now().Add(5*time.Minute)) {
			return fmt.Errorf("request audit: invalid latency or occurrence time")
		}
		if len(audit.TraceID) != 0 && len(audit.TraceID) != 32 {
			return fmt.Errorf("request audit: invalid trace ID")
		}
		if audit.DeploymentID != "" {
			if _, err := uuid.Parse(audit.DeploymentID); err != nil {
				return fmt.Errorf("request audit: invalid deployment ID: %w", err)
			}
		}
		if len(audit.CommitSHA) > 64 || len(audit.RequestID) > 128 {
			return fmt.Errorf("request audit: oversized revision or request ID")
		}
		if audit.SourceIP != "" {
			if _, err := netip.ParseAddr(audit.SourceIP); err != nil {
				return fmt.Errorf("request audit: invalid source IP: %w", err)
			}
		}
	}
	return nil
}

func consumerUsageBucketKey(accountID, appID, consumerKey string, minute time.Time) string {
	return accountID + "\x00" + appID + "\x00" + consumerKey + "\x00" + minute.UTC().Format(time.RFC3339)
}

func platformTenantUsageBucketKey(accountID, tenantID, appID, consumerKey string, minute time.Time) string {
	return accountID + "\x00" + tenantID + "\x00" + consumerUsageBucketKey(accountID, appID, consumerKey, minute)
}

// RecordAPIConsumerUsage applies one event exactly once and returns true when
// the event changed the aggregate. The MemStore implementation mirrors the
// Postgres event-ledger transaction and is used by handler tests.
func (m *MemStore) RecordAPIConsumerUsage(_ context.Context, event APIConsumerUsageEvent) (bool, error) {
	if err := ValidateAPIConsumerUsageEvent(event); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.apiConsumerUsageEvents[event.EventID]; exists {
		if event.Audit != nil {
			m.recordRequestAuditLocked(event)
		}
		return false, nil
	}
	if event.PlatformTenantID != "" {
		tenant, ok := m.platformTenants[event.PlatformTenantID]
		if !ok || tenant.AccountID != event.AccountID {
			return false, ErrNotFound
		}
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
	if event.PlatformTenantID != "" {
		tenantKey := platformTenantUsageBucketKey(event.AccountID, event.PlatformTenantID, event.AppID, event.ConsumerKey, event.WindowStart)
		tenantBucket := m.platformTenantUsage[tenantKey]
		if tenantBucket.AppID == "" {
			tenantBucket = APIConsumerUsageBucket{AccountID: event.AccountID, AppID: event.AppID,
				ConsumerKey: event.ConsumerKey, WindowStart: event.WindowStart.UTC()}
		}
		tenantBucket.RequestCount += event.RequestCount
		tenantBucket.ErrorCount += event.ErrorCount
		tenantBucket.BillableUnits += event.BillableUnits
		m.platformTenantUsage[tenantKey] = tenantBucket
	}
	m.apiConsumerUsageEvents[event.EventID] = struct{}{}
	if event.Audit != nil {
		m.recordRequestAuditLocked(event)
	}
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
