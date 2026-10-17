// Package debugcompare composes read-only retained deployment distributions.
package debugcompare

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type Stats struct {
	P50 int
	P95 int
	P99 int
	N   int64
}

type Fetch func(context.Context, string, time.Time, time.Time, string) (map[string]Stats, error)

// Build uses one captured clock and the same explicit window for both reads.
// The two sequential distributions are observations, not an atomic snapshot.
func Build(ctx context.Context, appID string, req api.DebugCompareRequest, retentionDays int, now time.Time, fetch Fetch) (api.DebugCompareResponse, *api.Problem) {
	out, problem := selection(appID, req, retentionDays, now)
	if problem != nil {
		return out, problem
	}
	start, _ := time.Parse(time.RFC3339Nano, out.WindowStart)
	end, _ := time.Parse(time.RFC3339Nano, out.WindowEnd)
	source, err := fetch(ctx, out.Source, start, end, req.Route)
	if err != nil {
		return out, api.ErrCapacity("compare source")
	}
	mirror, err := fetch(ctx, out.Mirror, start, end, req.Route)
	if err != nil {
		return out, api.ErrCapacity("compare mirror")
	}
	out.Routes = merge(source, mirror)
	return out, nil
}

func selection(appID string, req api.DebugCompareRequest, retentionDays int, now time.Time) (api.DebugCompareResponse, *api.Problem) {
	out := api.DebugCompareResponse{AppID: appID, Source: req.Source, Mirror: req.Mirror, RouteFilter: req.Route}
	source, sourceErr := uuid.Parse(req.Source)
	mirror, mirrorErr := uuid.Parse(req.Mirror)
	if sourceErr != nil || mirrorErr != nil || source == uuid.Nil || mirror == uuid.Nil {
		return out, api.ErrValidation("source and mirror must be nonzero deployment ids")
	}
	out.Source, out.Mirror = source.String(), mirror.String()
	duration, ok := lookback(req.Since)
	if !ok {
		return out, api.ErrValidation("since must be a positive duration or whole days")
	}
	end := now.UTC()
	if req.Until != "" {
		var err error
		end, err = time.Parse(time.RFC3339Nano, req.Until)
		if err != nil || end.After(now) {
			return out, api.ErrValidation("until must be an RFC3339 timestamp at or before now")
		}
	}
	start := end.Add(-duration)
	clamped := false
	if retentionDays > 0 {
		floor := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)
		if !end.After(floor) {
			return out, api.ErrValidation("comparison window ends outside retained telemetry")
		}
		if start.Before(floor) {
			start, clamped = floor, true
		}
	}
	out.Since = end.Sub(start).String()
	out.WindowStart, out.WindowEnd = start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano)
	out.RetentionClamped = &clamped
	return out, nil
}

func lookback(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Hour, true
	}
	if duration, err := time.ParseDuration(raw); err == nil && duration > 0 {
		return duration, true
	}
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.ParseInt(strings.TrimSuffix(raw, "d"), 10, 64)
		if err == nil && days > 0 && days <= int64(time.Duration(1<<63-1)/(24*time.Hour)) {
			return time.Duration(days) * 24 * time.Hour, true
		}
	}
	return 0, false
}
