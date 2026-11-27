package state

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/routeresources"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// InstanceResourceMinuteRow is one instance-minute of app cgroup usage with
// the identity vmmd stamped on it (ADR-20261121170000).
type InstanceResourceMinuteRow struct {
	AccountID    uuid.UUID
	DeploymentID uuid.UUID
	routeresources.InstanceMinute
}

// InstanceRouteMinuteRow is one route's activity on one instance-minute.
type InstanceRouteMinuteRow struct {
	AccountID uuid.UUID
	routeresources.RouteMinute
}

// RouteResourceStore keeps the minutes route resource peaks join. PgStore
// implements it; apid asserts it.
type RouteResourceStore interface {
	// AddInstanceResourceMinutes merges rows into their instance-minutes:
	// the first start stands, peaks and limits take the larger value, the
	// end takes the newer one and CPU time and samples add up.
	AddInstanceResourceMinutes(ctx context.Context, rows []InstanceResourceMinuteRow) error
	// AddInstanceRouteMinutes adds rows to their route minutes.
	AddInstanceRouteMinutes(ctx context.Context, rows []InstanceRouteMinuteRow) error
	// InstanceResourceMinutes returns an app's instance-minutes in
	// [from, to), newest first, at most limit; truncated reports more.
	InstanceResourceMinutes(ctx context.Context, appID uuid.UUID, from, to time.Time, limit int) (rows []routeresources.InstanceMinute, truncated bool, err error)
	// InstanceRouteMinutes returns an app's route minutes in [from, to).
	InstanceRouteMinutes(ctx context.Context, appID uuid.UUID, from, to time.Time, limit int) (rows []routeresources.RouteMinute, truncated bool, err error)
}

var _ RouteResourceStore = (*PgStore)(nil)

func (s *PgStore) AddInstanceResourceMinutes(ctx context.Context, rows []InstanceResourceMinuteRow) error {
	if len(rows) == 0 {
		return nil
	}
	n := len(rows)
	p := sqlc.AddInstanceResourceMinutesParams{
		AppIds: make([]pgtype.UUID, n), InstanceIds: make([]string, n), Minutes: make([]pgtype.Timestamptz, n),
		AccountIds: make([]pgtype.UUID, n), DeploymentIds: make([]pgtype.UUID, n), Samples: make([]int32, n),
		MemStartMb: make([]int32, n), MemPeakMb: make([]int32, n), MemEndMb: make([]int32, n), MemLimitMb: make([]int32, n),
		CpuMs: make([]int64, n), CpuPeakMillicores: make([]int32, n), CpuLimitMillicores: make([]int32, n),
	}
	for i, r := range rows {
		p.AppIds[i], p.AccountIds[i], p.DeploymentIds[i] = routeResourceUUID(r.AppID), routeResourceUUID(r.AccountID), routeResourceUUID(r.DeploymentID)
		p.InstanceIds[i], p.Minutes[i] = r.InstanceID, pgTimestamptzUTC(r.Minute.Truncate(time.Minute))
		p.Samples[i], p.MemStartMb[i], p.MemPeakMb[i] = int32(r.Samples), int32(r.MemStartMB), int32(r.MemPeakMB)
		p.MemEndMb[i], p.MemLimitMb[i] = int32(r.MemEndMB), int32(r.MemLimitMB)
		p.CpuMs[i], p.CpuPeakMillicores[i], p.CpuLimitMillicores[i] = r.CPUMS, int32(r.CPUPeakMillicores), int32(r.CPULimitMillicores)
	}
	if err := sqlc.New().AddInstanceResourceMinutes(ctx, s.pool, p); err != nil {
		return fmt.Errorf("state: add instance resource minutes: %w", err)
	}
	return nil
}

func (s *PgStore) AddInstanceRouteMinutes(ctx context.Context, rows []InstanceRouteMinuteRow) error {
	if len(rows) == 0 {
		return nil
	}
	n := len(rows)
	p := sqlc.AddInstanceRouteMinutesParams{
		AppIds: make([]pgtype.UUID, n), InstanceIds: make([]string, n), Minutes: make([]pgtype.Timestamptz, n),
		Routes: make([]string, n), Methods: make([]string, n), AccountIds: make([]pgtype.UUID, n),
		Requests: make([]int64, n), BusyMs: make([]int64, n), MaxLatencyMs: make([]int32, n),
	}
	for i, r := range rows {
		p.AppIds[i], p.AccountIds[i] = routeResourceUUID(r.AppID), routeResourceUUID(r.AccountID)
		p.InstanceIds[i], p.Minutes[i] = r.InstanceID, pgTimestamptzUTC(r.Minute.Truncate(time.Minute))
		p.Routes[i], p.Methods[i] = r.Route, r.Method
		p.Requests[i], p.BusyMs[i], p.MaxLatencyMs[i] = r.Requests, r.BusyMS, int32(r.MaxLatencyMS)
	}
	if err := sqlc.New().AddInstanceRouteMinutes(ctx, s.pool, p); err != nil {
		return fmt.Errorf("state: add instance route minutes: %w", err)
	}
	return nil
}

func (s *PgStore) InstanceResourceMinutes(ctx context.Context, appID uuid.UUID, from, to time.Time, limit int) ([]routeresources.InstanceMinute, bool, error) {
	rows, err := sqlc.New().ListInstanceResourceMinutes(ctx, s.pool, sqlc.ListInstanceResourceMinutesParams{
		AppID: routeResourceUUID(appID), FromMinute: pgTimestamptzUTC(from), ToMinute: pgTimestamptzUTC(to), RowLimit: int32(limit + 1),
	})
	if err != nil {
		return nil, false, fmt.Errorf("state: read instance resource minutes: %w", err)
	}
	out := make([]routeresources.InstanceMinute, 0, min(len(rows), limit))
	for _, r := range rows[:min(len(rows), limit)] {
		out = append(out, routeresources.InstanceMinute{
			AppID: appID, InstanceID: r.InstanceID, Minute: r.Minute.Time, Samples: int(r.Samples),
			MemStartMB: int(r.MemStartMb), MemPeakMB: int(r.MemPeakMb), MemEndMB: int(r.MemEndMb), MemLimitMB: int(r.MemLimitMb),
			CPUMS: r.CpuMs, CPUPeakMillicores: int(r.CpuPeakMillicores), CPULimitMillicores: int(r.CpuLimitMillicores),
		})
	}
	return out, len(rows) > limit, nil
}

func (s *PgStore) InstanceRouteMinutes(ctx context.Context, appID uuid.UUID, from, to time.Time, limit int) ([]routeresources.RouteMinute, bool, error) {
	rows, err := sqlc.New().ListInstanceRouteMinutes(ctx, s.pool, sqlc.ListInstanceRouteMinutesParams{
		AppID: routeResourceUUID(appID), FromMinute: pgTimestamptzUTC(from), ToMinute: pgTimestamptzUTC(to), RowLimit: int32(limit + 1),
	})
	if err != nil {
		return nil, false, fmt.Errorf("state: read instance route minutes: %w", err)
	}
	out := make([]routeresources.RouteMinute, 0, min(len(rows), limit))
	for _, r := range rows[:min(len(rows), limit)] {
		out = append(out, routeresources.RouteMinute{
			AppID: appID, InstanceID: r.InstanceID, Minute: r.Minute.Time, Route: r.Route, Method: r.Method,
			Requests: r.Requests, BusyMS: r.BusyMs, MaxLatencyMS: int(r.MaxLatencyMs),
		})
	}
	return out, len(rows) > limit, nil
}

func routeResourceUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
