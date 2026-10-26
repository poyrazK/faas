package wakehistory

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TraceRequest is one request the account trace view matched, as retained by
// request telemetry: ReceivedAt is its minute bucket (the gateway stamps the
// request's completion and truncates it to the minute) and LatencyMS the
// upper bound of its latency bucket.
type TraceRequest struct {
	AppID, WakeID string
	ColdBoot      bool
	ReceivedAt    time.Time
	LatencyMS     int
}

// traceClockSlack absorbs clock differences between the gateway that stamps
// a request and schedd that stamps its wake.
const traceClockSlack = 2 * time.Second

// TraceWakes reads, for each request, the wake it waited for: the wake method
// and how long the instance took from boot start to ready, from the same
// retained history as the wake timeline. A request that resumed its wake's
// frozen instance (ADR-1151) is marked thawed with the thaw's duration
// instead, because a thaw keeps the instance's original wake ID (Hunt 12
// H12-49). Warm requests, and cold ones whose wake history is gone, get nil;
// a cold request that started after its wake's boot finished gets the wake
// ID only. At most api.AccountTraceWakesMax wakes are read. The result is
// indexed like requests, or nil when no request waited for a retained wake
// or the store lacks the capability.
func TraceWakes(ctx context.Context, store any, requests []TraceRequest) ([]*api.AccountTraceWake, error) {
	reader, ok := store.(state.WakeHistoryByWakeIDsStore)
	if !ok {
		return nil, nil
	}
	var appIDs, wakeIDs []string
	for _, r := range requests {
		// Wake and app ids are UUIDs; anything else names no retained wake and
		// must not fail the read for the others.
		if !r.ColdBoot || !validID(r.AppID) || !validID(r.WakeID) || slices.Contains(wakeIDs, r.WakeID) || len(wakeIDs) == api.AccountTraceWakesMax {
			continue
		}
		wakeIDs = append(wakeIDs, r.WakeID)
		if !slices.Contains(appIDs, r.AppID) {
			appIDs = append(appIDs, r.AppID)
		}
	}
	if len(wakeIDs) == 0 {
		return nil, nil
	}
	records, err := reader.ListWakeHistoryByWakeIDs(ctx, appIDs, wakeIDs)
	if err != nil {
		return nil, err
	}
	wakes := make(map[string]tracedWake, len(records))
	for _, record := range records {
		wake := api.AccountTraceWake{WakeID: record.WakeID}
		if row, err := project(record); err == nil {
			wake.Method, wake.PlannedMethod, wake.RestoreFallbackReason = row.Method, row.PlannedMethod, row.RestoreFallbackReason
			if row.ReadyInMS > 0 {
				wake.ReadyInMS = row.ReadyInMS
			}
		}
		wakes[record.WakeID] = tracedWake{wake: wake, completedAt: record.CompletedAt}
	}
	thaws, err := traceThaws(ctx, store, requests, appIDs, wakeIDs)
	if err != nil {
		return nil, err
	}
	out := make([]*api.AccountTraceWake, len(requests))
	for i, r := range requests {
		known, ok := wakes[r.WakeID]
		if !r.ColdBoot || !ok {
			continue
		}
		wake := known.attribute(r, thaws)
		out[i] = &wake
	}
	return out, nil
}

type tracedWake struct {
	wake        api.AccountTraceWake
	completedAt time.Time
}

// attribute says what request r waited for. The request ended inside its
// minute bucket and began at most its latency before that, so a thaw of its
// wake answered in that window is the one it resumed; a boot that completed
// before the window opened is one it never waited for.
func (w tracedWake) attribute(r TraceRequest, thaws []state.WakeThawRecord) api.AccountTraceWake {
	earliest := r.ReceivedAt.Add(-time.Duration(r.LatencyMS)*time.Millisecond - traceClockSlack)
	latest := r.ReceivedAt.Add(time.Minute + traceClockSlack)
	for _, thaw := range thaws {
		if thaw.WakeID == r.WakeID && !thaw.At.Before(earliest) && !thaw.At.After(latest) {
			return api.AccountTraceWake{WakeID: r.WakeID, Thawed: true, ThawMS: int32(min(max(thaw.TotalMs, 0), int64(time.Hour/time.Millisecond)))}
		}
	}
	if !w.completedAt.IsZero() && w.completedAt.Before(earliest) {
		return api.AccountTraceWake{WakeID: r.WakeID}
	}
	return w.wake
}

// traceThaws reads the thaws of the requested wakes across every cold
// request's window, when the store can.
func traceThaws(ctx context.Context, store any, requests []TraceRequest, appIDs, wakeIDs []string) ([]state.WakeThawRecord, error) {
	reader, ok := store.(state.WakeThawsStore)
	if !ok {
		return nil, nil
	}
	var since, until time.Time
	for _, r := range requests {
		if !r.ColdBoot || r.ReceivedAt.IsZero() || !slices.Contains(wakeIDs, r.WakeID) {
			continue
		}
		from := r.ReceivedAt.Add(-time.Duration(r.LatencyMS)*time.Millisecond - traceClockSlack)
		to := r.ReceivedAt.Add(time.Minute + traceClockSlack)
		if since.IsZero() || from.Before(since) {
			since = from
		}
		if to.After(until) {
			until = to
		}
	}
	if since.IsZero() {
		return nil, nil
	}
	return reader.ListWakeThaws(ctx, appIDs, wakeIDs, since, until)
}

func validID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil
}
