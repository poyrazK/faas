package state

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// App-reported units (ADR-1318): a card with unit "app:<meter>" prices the
// units the app reports for meter in its Gregale-Usage response header or
// trailer, such as tokens or images.
const AppUnitPrefix = "app:"

// Bounds on what one request can report (ADR-1318).
const (
	MaxAppMetersPerRequest     = 8
	MaxAppMeterUnitsPerRequest = int64(1_000_000_000_000)
)

var appMeterRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// ValidAppMeter reports whether name is a meter an app can report.
func ValidAppMeter(name string) bool { return appMeterRE.MatchString(name) }

// AppMeterOfUnit returns the meter of an app unit.
func AppMeterOfUnit(unit string) (string, bool) {
	meter, ok := strings.CutPrefix(unit, AppUnitPrefix)
	return meter, ok && ValidAppMeter(meter)
}

// APIConsumerMeterUsageBucket is one consumer's reported units for one
// meter in one minute.
type APIConsumerMeterUsageBucket struct {
	WindowStart time.Time
	Meter       string
	Units       int64
}

// ConsumerMeterUsageStore reads a consumer's app-reported units (ADR-1318).
type ConsumerMeterUsageStore interface {
	ListAPIConsumerMeterUsage(ctx context.Context, accountID, appID, consumerKey, meter string, since, until time.Time) ([]APIConsumerMeterUsageBucket, error)
}

var (
	_ ConsumerMeterUsageStore = (*MemStore)(nil)
	_ ConsumerMeterUsageStore = (*PgStore)(nil)
)

// validateMeterUnits checks an event's reported units: billable consumer
// requests only, at most MaxAppMetersPerRequest meters per request, and
// bounded non-negative units.
func validateMeterUnits(event APIConsumerUsageEvent) error {
	if len(event.MeterUnits) == 0 {
		return nil
	}
	if event.ConsumerKey == AnonymousConsumerKey || event.BillableUnits == 0 {
		return fmt.Errorf("consumer usage: app-reported units belong to billable consumer requests")
	}
	if int64(len(event.MeterUnits)) > event.RequestCount*MaxAppMetersPerRequest {
		return fmt.Errorf("consumer usage: at most %d app meters per request", MaxAppMetersPerRequest)
	}
	for meter, units := range event.MeterUnits {
		if !ValidAppMeter(meter) || units < 0 || units > event.RequestCount*MaxAppMeterUnitsPerRequest {
			return fmt.Errorf("consumer usage: app meter %q must be a lowercase name with 0 to %d units per request", meter, MaxAppMeterUnitsPerRequest)
		}
	}
	return nil
}

func consumerMeterUsageKey(accountID, appID, consumerKey, meter string, minute time.Time) string {
	return accountID + "\x00" + appID + "\x00" + consumerKey + "\x00" + meter + "\x00" + minute.UTC().Format(time.RFC3339)
}

// recordMeterUnitsLocked adds an event's reported units to the ledger.
func (m *MemStore) recordMeterUnitsLocked(event APIConsumerUsageEvent) {
	if len(event.MeterUnits) == 0 {
		return
	}
	if m.apiConsumerMeterUsage == nil {
		m.apiConsumerMeterUsage = map[string]APIConsumerMeterUsageBucket{}
	}
	for meter, units := range event.MeterUnits {
		key := consumerMeterUsageKey(event.AccountID, event.AppID, event.ConsumerKey, meter, event.WindowStart)
		bucket := m.apiConsumerMeterUsage[key]
		bucket.WindowStart, bucket.Meter = event.WindowStart.UTC(), meter
		bucket.Units += units
		m.apiConsumerMeterUsage[key] = bucket
	}
}

func (m *MemStore) ListAPIConsumerMeterUsage(_ context.Context, accountID, appID, consumerKey, meter string, since, until time.Time) ([]APIConsumerMeterUsageBucket, error) {
	if accountID == "" || appID == "" || consumerKey == "" || !until.After(since) {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := accountID + "\x00" + appID + "\x00" + consumerKey + "\x00" + meter + "\x00"
	var out []APIConsumerMeterUsageBucket
	for key, bucket := range m.apiConsumerMeterUsage {
		if strings.HasPrefix(key, prefix) && !bucket.WindowStart.Before(since) && bucket.WindowStart.Before(until) {
			out = append(out, bucket)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WindowStart.Before(out[j].WindowStart) })
	return out, nil
}

func (s *PgStore) ListAPIConsumerMeterUsage(ctx context.Context, accountID, appID, consumerKey, meter string, since, until time.Time) ([]APIConsumerMeterUsageBucket, error) {
	if accountID == "" || appID == "" || consumerKey == "" || !until.After(since) {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `select window_start, meter, units from api_consumer_meter_usage_minutes
		where account_id = $1::uuid and app_id = $2::uuid and consumer_key = $3 and meter = $4
		  and window_start >= $5 and window_start < $6
		order by window_start`, accountID, appID, consumerKey, meter, since.UTC(), until.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIConsumerMeterUsageBucket
	for rows.Next() {
		var b APIConsumerMeterUsageBucket
		if err := rows.Scan(&b.WindowStart, &b.Meter, &b.Units); err != nil {
			return nil, err
		}
		b.WindowStart = b.WindowStart.UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}
