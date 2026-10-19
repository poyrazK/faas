package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	usagesqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

// ADR-1323: realtime usage is recorded per endpoint, node and hour to review
// the fair-use limits. Realtime is free; nothing bills from it.

// RealtimeUsageMinutes is the number of per-minute peaks in one hourly row.
const RealtimeUsageMinutes = 60

// ErrRealtimeUsageInvalid rejects a malformed or out-of-window usage report.
var ErrRealtimeUsageInvalid = errors.New("state: invalid realtime usage report")

// RealtimeEndpointUsage is one endpoint's cumulative usage on one realtimed
// process for one UTC hour. MinutePeakConnections[i] is the most connections
// open at once on that process during minute i of the hour. Delivered counts
// cover frames that carry customer payloads (channel, direct and inbox
// messages and signals), one per recipient; received counts cover client
// messages forwarded to the app.
type RealtimeEndpointUsage struct {
	EndpointID            string  `json:"endpoint_id"`
	MinutePeakConnections []int32 `json:"minute_peak_connections"`
	ConnectionSeconds     int64   `json:"connection_seconds"`
	DeliveredMessages     int64   `json:"delivered_messages"`
	DeliveredBytes        int64   `json:"delivered_bytes"`
	ReceivedMessages      int64   `json:"received_messages"`
	ReceivedBytes         int64   `json:"received_bytes"`
}

// RealtimeUsageReport is one realtimed process's usage for one UTC hour.
// Incarnation identifies the process run, so a restart starts new rows rather
// than lowering the cumulative values of the previous run.
type RealtimeUsageReport struct {
	NodeName    string                  `json:"node_name"`
	Incarnation string                  `json:"incarnation"`
	Hour        time.Time               `json:"hour"`
	Endpoints   []RealtimeEndpointUsage `json:"endpoints"`
}

// RealtimeUsageHour is one endpoint's fleet-wide usage for one UTC hour.
// PeakConnections is the largest per-minute sum of the nodes' peaks.
type RealtimeUsageHour struct {
	EndpointID        string    `json:"endpoint_id"`
	AppID             string    `json:"app_id"`
	Hour              time.Time `json:"hour"`
	PeakConnections   int64     `json:"peak_connections"`
	ConnectionSeconds int64     `json:"connection_seconds"`
	DeliveredMessages int64     `json:"delivered_messages"`
	DeliveredBytes    int64     `json:"delivered_bytes"`
	ReceivedMessages  int64     `json:"received_messages"`
	ReceivedBytes     int64     `json:"received_bytes"`
}

// RealtimeUsageStore records and reads realtime usage.
type RealtimeUsageStore interface {
	RecordRealtimeUsage(context.Context, RealtimeUsageReport) (int, error)
	ReadRealtimeUsage(ctx context.Context, accountID string, from, to time.Time) ([]RealtimeUsageHour, error)
}

// ValidateRealtimeUsageReport checks a report against now. The hour must be a
// whole UTC hour no older than api.RealtimeUsageReportMaxAge and not in the
// future.
func ValidateRealtimeUsageReport(r RealtimeUsageReport, now time.Time) error {
	if len(r.NodeName) == 0 || len(r.NodeName) > 253 {
		return fmt.Errorf("%w: node name", ErrRealtimeUsageInvalid)
	}
	if _, err := uuid.Parse(r.Incarnation); err != nil {
		return fmt.Errorf("%w: incarnation", ErrRealtimeUsageInvalid)
	}
	hour := r.Hour.UTC()
	if !hour.Equal(hour.Truncate(time.Hour)) || hour.After(now) || now.Sub(hour) > api.RealtimeUsageReportMaxAge+time.Hour {
		return fmt.Errorf("%w: hour", ErrRealtimeUsageInvalid)
	}
	if len(r.Endpoints) == 0 || len(r.Endpoints) > api.RealtimeUsageReportMaxEndpoints {
		return fmt.Errorf("%w: endpoint count", ErrRealtimeUsageInvalid)
	}
	seen := make(map[string]bool, len(r.Endpoints))
	for _, e := range r.Endpoints {
		id, err := uuid.Parse(e.EndpointID)
		if err != nil || seen[id.String()] {
			return fmt.Errorf("%w: endpoint id", ErrRealtimeUsageInvalid)
		}
		seen[id.String()] = true
		if len(e.MinutePeakConnections) != RealtimeUsageMinutes || e.ConnectionSeconds < 0 || e.DeliveredMessages < 0 ||
			e.DeliveredBytes < 0 || e.ReceivedMessages < 0 || e.ReceivedBytes < 0 {
			return fmt.Errorf("%w: endpoint values", ErrRealtimeUsageInvalid)
		}
		for _, peak := range e.MinutePeakConnections {
			if peak < 0 {
				return fmt.Errorf("%w: endpoint values", ErrRealtimeUsageInvalid)
			}
		}
	}
	return nil
}

// RecordRealtimeUsage stores a report and returns how many endpoint rows it
// recorded. Endpoints that no longer exist are skipped. Each row keeps the
// larger of its stored and reported values, so a report may be sent any
// number of times, in any order.
func (s *PgStore) RecordRealtimeUsage(ctx context.Context, r RealtimeUsageReport) (int, error) {
	if err := ValidateRealtimeUsageReport(r, time.Now()); err != nil {
		return 0, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("state: record realtime usage: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := usagesqlc.New()
	recorded := 0
	for _, e := range r.Endpoints {
		n, err := q.RecordRealtimeUsage(ctx, tx, usagesqlc.RecordRealtimeUsageParams{
			Hour: pgtype.Timestamptz{Time: r.Hour.UTC(), Valid: true}, NodeName: r.NodeName,
			Incarnation: realtimeUsageUUID(r.Incarnation), EndpointID: realtimeUsageUUID(e.EndpointID),
			MinutePeakConnections: e.MinutePeakConnections, ConnectionSeconds: e.ConnectionSeconds,
			DeliveredMessages: e.DeliveredMessages, DeliveredBytes: e.DeliveredBytes,
			ReceivedMessages: e.ReceivedMessages, ReceivedBytes: e.ReceivedBytes,
		})
		if err != nil {
			return 0, fmt.Errorf("state: record realtime usage for endpoint %s: %w", e.EndpointID, err)
		}
		recorded += int(n)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: record realtime usage: %w", err)
	}
	return recorded, nil
}

// ReadRealtimeUsage returns an account's fleet-wide usage per endpoint and
// hour for hours in [from, to), oldest first, capped at
// api.RealtimeUsageReadMaxRows rows.
func (s *PgStore) ReadRealtimeUsage(ctx context.Context, accountID string, from, to time.Time) ([]RealtimeUsageHour, error) {
	if _, err := uuid.Parse(accountID); err != nil || !from.Before(to) {
		return nil, ErrInvalidArgument
	}
	rows, err := usagesqlc.New().ReadRealtimeUsageHours(ctx, s.pool, usagesqlc.ReadRealtimeUsageHoursParams{
		AccountID: realtimeUsageUUID(accountID), FromHour: pgtype.Timestamptz{Time: from, Valid: true},
		ToHour: pgtype.Timestamptz{Time: to, Valid: true}, RowLimit: api.RealtimeUsageReadMaxRows,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return []RealtimeUsageHour{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("state: read realtime usage: %w", err)
	}
	out := make([]RealtimeUsageHour, 0, len(rows))
	for _, row := range rows {
		out = append(out, RealtimeUsageHour{
			EndpointID: row.EndpointID, AppID: row.AppID, Hour: row.Hour.Time.UTC(), PeakConnections: row.PeakConnections,
			ConnectionSeconds: row.ConnectionSeconds, DeliveredMessages: row.DeliveredMessages, DeliveredBytes: row.DeliveredBytes,
			ReceivedMessages: row.ReceivedMessages, ReceivedBytes: row.ReceivedBytes,
		})
	}
	return out, nil
}

// realtimeUsageUUID converts an already-validated UUID in any form uuid.Parse
// accepts.
func realtimeUsageUUID(value string) pgtype.UUID {
	id, err := uuid.Parse(value)
	return pgtype.UUID{Bytes: id, Valid: err == nil}
}

// RealtimeAccountUsageHour is an account's fleet-wide usage for one UTC hour
// across all of its endpoints.
type RealtimeAccountUsageHour struct {
	Hour              time.Time `json:"hour"`
	PeakConnections   int64     `json:"peak_connections"`
	ConnectionSeconds int64     `json:"connection_seconds"`
	DeliveredMessages int64     `json:"delivered_messages"`
	DeliveredBytes    int64     `json:"delivered_bytes"`
	ReceivedMessages  int64     `json:"received_messages"`
	ReceivedBytes     int64     `json:"received_bytes"`
}

// RealtimeAccountUsageReader serves the customer usage view (ADR-1323).
type RealtimeAccountUsageReader interface {
	ReadRealtimeUsage(ctx context.Context, accountID string, from, to time.Time) ([]RealtimeUsageHour, error)
	ReadRealtimeAccountUsage(ctx context.Context, accountID string, from, to time.Time) ([]RealtimeAccountUsageHour, error)
	// ReadRealtimeOpenConnections is the account's open connections on nodes
	// that reported within api.RealtimeConnectionSyncStaleAfter.
	ReadRealtimeOpenConnections(ctx context.Context, accountID string) (int64, error)
}

// ReadRealtimeAccountUsage returns an account's usage per hour in [from, to),
// oldest first.
func (s *PgStore) ReadRealtimeAccountUsage(ctx context.Context, accountID string, from, to time.Time) ([]RealtimeAccountUsageHour, error) {
	if _, err := uuid.Parse(accountID); err != nil || !from.Before(to) {
		return nil, ErrInvalidArgument
	}
	rows, err := usagesqlc.New().ReadRealtimeAccountUsageHours(ctx, s.pool, usagesqlc.ReadRealtimeAccountUsageHoursParams{
		AccountID: realtimeUsageUUID(accountID), FromHour: pgtype.Timestamptz{Time: from, Valid: true},
		ToHour: pgtype.Timestamptz{Time: to, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("state: read realtime account usage: %w", err)
	}
	out := make([]RealtimeAccountUsageHour, 0, len(rows))
	for _, row := range rows {
		out = append(out, RealtimeAccountUsageHour{
			Hour: row.Hour.Time.UTC(), PeakConnections: row.PeakConnections, ConnectionSeconds: row.ConnectionSeconds,
			DeliveredMessages: row.DeliveredMessages, DeliveredBytes: row.DeliveredBytes,
			ReceivedMessages: row.ReceivedMessages, ReceivedBytes: row.ReceivedBytes,
		})
	}
	return out, nil
}

// ReadRealtimeOpenConnections sums the account's freshly reported connections.
func (s *PgStore) ReadRealtimeOpenConnections(ctx context.Context, accountID string) (int64, error) {
	if _, err := uuid.Parse(accountID); err != nil {
		return 0, ErrInvalidArgument
	}
	n, err := usagesqlc.New().ReadAccountOpenConnections(ctx, s.pool, usagesqlc.ReadAccountOpenConnectionsParams{
		AccountID: realtimeUsageUUID(accountID), StaleSeconds: api.RealtimeConnectionSyncStaleAfter.Seconds(),
	})
	if err != nil {
		return 0, fmt.Errorf("state: read realtime open connections: %w", err)
	}
	return n, nil
}
