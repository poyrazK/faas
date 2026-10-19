package state

import (
	"context"
	"sort"
	"strings"
	"time"
)

// PlatformTenantMeterUsageBucket is one tenant subject's app-reported units
// for one meter in one minute (ADR-1319). SourceKind is consumer, surface or
// jwt, and SubjectKey the consumer, surface or JWT rule ID.
type PlatformTenantMeterUsageBucket struct {
	AppID       string
	SourceKind  string
	SubjectKey  string
	WindowStart time.Time
	Meter       string
	Units       int64
}

// PlatformTenantMeterUsageStore reads a tenant's app-reported units.
type PlatformTenantMeterUsageStore interface {
	ListPlatformTenantMeterUsage(ctx context.Context, accountID, tenantID, meter string, since, until time.Time) ([]PlatformTenantMeterUsageBucket, error)
}

var (
	_ PlatformTenantMeterUsageStore = (*MemStore)(nil)
	_ PlatformTenantMeterUsageStore = (*PgStore)(nil)
)

// tenantUsageSubject is the source kind and subject an event's tenant
// usage is recorded under.
func tenantUsageSubject(event APIConsumerUsageEvent) (string, string) {
	switch {
	case event.PlatformTenantSurfaceID != "":
		return "surface", event.PlatformTenantSurfaceID
	case event.PlatformTenantJWTAuthorizationRuleID != "":
		return "jwt", event.PlatformTenantJWTAuthorizationRuleID
	}
	return "consumer", event.ConsumerKey
}

func tenantMeterUsageKey(accountID, tenantID, meter, appID, kind, subject string, minute time.Time) string {
	return accountID + "\x00" + tenantID + "\x00" + meter + "\x00" + appID + "\x00" + kind + "\x00" + subject + "\x00" +
		minute.UTC().Format(time.RFC3339)
}

// recordTenantMeterUnitsLocked adds an event's reported units to its
// tenant's ledger.
func (m *MemStore) recordTenantMeterUnitsLocked(event APIConsumerUsageEvent) {
	if event.PlatformTenantID == "" || len(event.MeterUnits) == 0 {
		return
	}
	if m.platformTenantMeterUsage == nil {
		m.platformTenantMeterUsage = map[string]PlatformTenantMeterUsageBucket{}
	}
	kind, subject := tenantUsageSubject(event)
	for meter, units := range event.MeterUnits {
		key := tenantMeterUsageKey(event.AccountID, event.PlatformTenantID, meter, event.AppID, kind, subject, event.WindowStart)
		bucket := m.platformTenantMeterUsage[key]
		bucket.AppID, bucket.SourceKind, bucket.SubjectKey = event.AppID, kind, subject
		bucket.WindowStart, bucket.Meter = event.WindowStart.UTC(), meter
		bucket.Units += units
		m.platformTenantMeterUsage[key] = bucket
	}
}

func (m *MemStore) ListPlatformTenantMeterUsage(_ context.Context, accountID, tenantID, meter string, since, until time.Time) ([]PlatformTenantMeterUsageBucket, error) {
	if accountID == "" || tenantID == "" || !until.After(since) {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := accountID + "\x00" + tenantID + "\x00" + meter + "\x00"
	var out []PlatformTenantMeterUsageBucket
	for key, bucket := range m.platformTenantMeterUsage {
		if strings.HasPrefix(key, prefix) && !bucket.WindowStart.Before(since) && bucket.WindowStart.Before(until) {
			out = append(out, bucket)
		}
	}
	sortTenantMeterUsage(out)
	return out, nil
}

func sortTenantMeterUsage(out []PlatformTenantMeterUsageBucket) {
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.WindowStart.Equal(b.WindowStart) {
			return a.WindowStart.Before(b.WindowStart)
		}
		return a.AppID+a.SourceKind+a.SubjectKey < b.AppID+b.SourceKind+b.SubjectKey
	})
}

func (s *PgStore) ListPlatformTenantMeterUsage(ctx context.Context, accountID, tenantID, meter string, since, until time.Time) ([]PlatformTenantMeterUsageBucket, error) {
	if accountID == "" || tenantID == "" || !until.After(since) {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `select app_id::text, source_kind, subject_key, window_start, meter, units
		from platform_tenant_meter_usage_minutes
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and meter = $3
		  and window_start >= $4 and window_start < $5
		order by window_start, app_id, source_kind, subject_key`, accountID, tenantID, meter, since.UTC(), until.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlatformTenantMeterUsageBucket
	for rows.Next() {
		var b PlatformTenantMeterUsageBucket
		if err := rows.Scan(&b.AppID, &b.SourceKind, &b.SubjectKey, &b.WindowStart, &b.Meter, &b.Units); err != nil {
			return nil, err
		}
		b.WindowStart = b.WindowStart.UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}
