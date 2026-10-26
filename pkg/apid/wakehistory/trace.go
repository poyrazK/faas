package wakehistory

import (
	"context"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TraceWakes reads how each wake in wakeIDs was woken, keyed by wake ID, for
// the account trace view: the wake method and how long the instance took from
// boot start to ready, from the same retained history as the wake timeline.
// Only wakes of appIDs are read, at most api.AccountTraceWakesMax of them. A
// store without the capability returns nothing; a wake whose metadata is
// missing or inconsistent keeps only its ID.
func TraceWakes(ctx context.Context, store any, appIDs, wakeIDs []string) (map[string]api.AccountTraceWake, error) {
	reader, ok := store.(state.WakeHistoryByWakeIDsStore)
	// Wake and app ids are UUIDs; anything else names no retained wake and
	// must not fail the read for the others.
	appIDs, wakeIDs = validIDs(appIDs), validIDs(wakeIDs)
	if !ok || len(appIDs) == 0 || len(wakeIDs) == 0 {
		return nil, nil
	}
	if len(wakeIDs) > api.AccountTraceWakesMax {
		wakeIDs = wakeIDs[:api.AccountTraceWakesMax]
	}
	records, err := reader.ListWakeHistoryByWakeIDs(ctx, appIDs, wakeIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]api.AccountTraceWake, len(records))
	for _, record := range records {
		wake := api.AccountTraceWake{WakeID: record.WakeID}
		if row, err := project(record); err == nil {
			wake.Method, wake.PlannedMethod, wake.RestoreFallbackReason = row.Method, row.PlannedMethod, row.RestoreFallbackReason
			if row.ReadyInMS > 0 {
				wake.ReadyInMS = row.ReadyInMS
			}
		}
		out[record.WakeID] = wake
	}
	return out, nil
}

func validIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := uuid.Parse(id); err == nil {
			out = append(out, id)
		}
	}
	return out
}
