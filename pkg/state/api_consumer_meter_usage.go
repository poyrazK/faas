package state

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/lazyregexp"
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

// MaxAPIConsumerModelPrices bounds the number of overrides in a model-priced card.
const MaxAPIConsumerModelPrices = 16

// ModelMeterName is the reported meter name for one configured model. Keeping
// model-specific reports in the existing wire format makes them replayable by
// all gateway and receiver versions that support app meters.
func ModelMeterName(meter, model string) string { return meter + "__" + model }

func ValidateAPIConsumerModelPrices(unit string, prices map[string]int64, tierCount int) error {
	if len(prices) == 0 {
		return nil
	}
	meter, ok := AppMeterOfUnit(unit)
	if !ok || strings.Contains(meter, "__") || tierCount != 0 || len(prices) > MaxAPIConsumerModelPrices {
		return fmt.Errorf("%w: model_prices requires a flat app meter with at most %d model overrides", ErrInvalidArgument, MaxAPIConsumerModelPrices)
	}
	for model, price := range prices {
		if !ValidAppMeter(model) || strings.Contains(model, "__") || !ValidAppMeter(ModelMeterName(meter, model)) || price < 0 {
			return fmt.Errorf("%w: model %q must form a valid reported meter and have a non-negative price", ErrInvalidArgument, model)
		}
	}
	return nil
}

// CheckModelMeterOverlap keeps a report from being rated both as a standalone
// meter and as a model override in the same app.
func CheckModelMeterOverlap(unit string, prices map[string]int64, existingUnit string, existingPrices map[string]int64) error {
	meter, ok := AppMeterOfUnit(unit)
	existingMeter, existingOK := AppMeterOfUnit(existingUnit)
	if !ok || !existingOK || meter == existingMeter {
		return nil
	}
	for model := range prices {
		if ModelMeterName(meter, model) == existingMeter {
			return fmt.Errorf("%w: model meter %q already has a standalone rate card", ErrInvalidArgument, existingMeter)
		}
	}
	for model := range existingPrices {
		if ModelMeterName(existingMeter, model) == meter {
			return fmt.Errorf("%w: meter %q already belongs to %q", ErrInvalidArgument, meter, existingMeter)
		}
	}
	return nil
}

var appMeterRE = lazyregexp.New(`^[a-z][a-z0-9_]{0,31}$`)

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

// validateMeterUnits checks an event's reported units: billable consumer or
// tenant surface and JWT-rule requests only (ADR-1319), at most
// MaxAppMetersPerRequest meters per request, and bounded non-negative
// units.
func validateMeterUnits(event APIConsumerUsageEvent) error {
	if len(event.MeterUnits) == 0 {
		return nil
	}
	tenantSource := event.PlatformTenantSurfaceID != "" || event.PlatformTenantJWTAuthorizationRuleID != ""
	if (event.ConsumerKey == AnonymousConsumerKey && !tenantSource) || event.BillableUnits == 0 {
		return fmt.Errorf("consumer usage: app-reported units belong to billable consumer or tenant-source requests")
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

// recordMeterUnitsLocked adds an event's reported units to the consumer
// ledger and its tenant's (ADR-1319).
func (m *MemStore) recordMeterUnitsLocked(event APIConsumerUsageEvent) {
	m.recordTenantMeterUnitsLocked(event)
	if len(event.MeterUnits) == 0 || event.ConsumerKey == AnonymousConsumerKey {
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
	prefix := accountID + "\x00" + appID + "\x00" + consumerKey + "\x00"
	if meter != "" {
		prefix += meter + "\x00"
	}
	var out []APIConsumerMeterUsageBucket
	for key, bucket := range m.apiConsumerMeterUsage {
		if strings.HasPrefix(key, prefix) && !bucket.WindowStart.Before(since) && bucket.WindowStart.Before(until) {
			out = append(out, bucket)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].WindowStart.Equal(out[j].WindowStart) {
			return out[i].Meter < out[j].Meter
		}
		return out[i].WindowStart.Before(out[j].WindowStart)
	})
	return out, nil
}

func (s *PgStore) ListAPIConsumerMeterUsage(ctx context.Context, accountID, appID, consumerKey, meter string, since, until time.Time) ([]APIConsumerMeterUsageBucket, error) {
	if accountID == "" || appID == "" || consumerKey == "" || !until.After(since) {
		return nil, ErrNotFound
	}
	query := `select window_start, meter, units from api_consumer_meter_usage_minutes
		where account_id = $1::uuid and app_id = $2::uuid and consumer_key = $3
		  and window_start >= $4 and window_start < $5`
	args := []any{accountID, appID, consumerKey, since.UTC(), until.UTC()}
	if meter != "" {
		query += ` and meter = $6`
		args = append(args, meter)
	}
	rows, err := s.pool.Query(ctx, query+` order by window_start, meter`, args...)
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
