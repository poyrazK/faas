package state

import (
	"context"
	"sort"
	"time"
)

// APIConsumerTelemetryHour is one consumer's successful (status < 400)
// requests in one UTC hour as request telemetry observed them (ADR-848).
type APIConsumerTelemetryHour struct {
	Hour               time.Time
	SuccessfulRequests int64
}

// ConsumerUsageTelemetryStore reads the independent request-telemetry
// evidence a usage-completeness check compares the billing ledger with.
type ConsumerUsageTelemetryStore interface {
	ListAPIConsumerTelemetryHours(ctx context.Context, accountID, appID, consumerID string, since, until time.Time) ([]APIConsumerTelemetryHour, error)
}

var (
	_ ConsumerUsageTelemetryStore = (*MemStore)(nil)
	_ ConsumerUsageTelemetryStore = (*PgStore)(nil)
)

func (s *PgStore) ListAPIConsumerTelemetryHours(ctx context.Context, accountID, appID, consumerID string, since, until time.Time) ([]APIConsumerTelemetryHour, error) {
	if accountID == "" || appID == "" || consumerID == "" || !until.After(since) {
		return nil, ErrInvalidArgument
	}
	rows, err := s.pool.Query(ctx, `
		select date_trunc('hour', received_at) as hour, sum(count)::bigint
		  from request_telemetry
		 where app_id = $2::uuid and consumer_id = $3::uuid and account_id = $1::uuid
		   and received_at >= $4 and received_at < $5 and status < 400
		 group by 1 order by 1`, accountID, appID, consumerID, since.UTC(), until.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIConsumerTelemetryHour
	for rows.Next() {
		var hour APIConsumerTelemetryHour
		if err := rows.Scan(&hour.Hour, &hour.SuccessfulRequests); err != nil {
			return nil, err
		}
		hour.Hour = hour.Hour.UTC()
		out = append(out, hour)
	}
	return out, rows.Err()
}

// SeedAPIConsumerTelemetryHour records telemetry evidence in the in-memory
// store, which otherwise keeps no request telemetry. Tests use it to drive
// completeness checks.
func (m *MemStore) SeedAPIConsumerTelemetryHour(accountID, appID, consumerID string, hour time.Time, successful int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.apiConsumerTelemetryHours == nil {
		m.apiConsumerTelemetryHours = map[string]APIConsumerTelemetryHour{}
	}
	hour = hour.UTC().Truncate(time.Hour)
	key := accountID + "\x00" + appID + "\x00" + consumerID + "\x00" + hour.Format(time.RFC3339)
	entry := m.apiConsumerTelemetryHours[key]
	entry.Hour = hour
	entry.SuccessfulRequests += successful
	m.apiConsumerTelemetryHours[key] = entry
}

func (m *MemStore) ListAPIConsumerTelemetryHours(_ context.Context, accountID, appID, consumerID string, since, until time.Time) ([]APIConsumerTelemetryHour, error) {
	if accountID == "" || appID == "" || consumerID == "" || !until.After(since) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := accountID + "\x00" + appID + "\x00" + consumerID + "\x00"
	var out []APIConsumerTelemetryHour
	for key, hour := range m.apiConsumerTelemetryHours {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix && !hour.Hour.Before(since) && hour.Hour.Before(until) {
			out = append(out, hour)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hour.Before(out[j].Hour) })
	return out, nil
}
