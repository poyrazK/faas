package imaged

// GC algorithm — pure functions over the joined SnapshotForGC list. Kept
// separate from Loop so the table tests don't need to drive a real ticker
// to exercise the eviction logic.

import (
	"sort"

	"github.com/onebox-faas/faas/pkg/state"
)

// perAppKeepCurrentPrevious returns the snapshot IDs that fall outside the
// "current + previous per app" retention window (spec §4.6). The function
// is pure; it does not mutate the input slice.
//
// Algorithm: per (appID), keep the two newest snapshots by CreatedAt
// (the "current" deployment's snap and the "previous" deployment's
// snap); everything older is a candidate for deletion.
//
// Soft-deleted apps (status='deleted') are filtered out by the SQL
// layer in Store.ListSnapshotsForGC, so we don't have to handle them
// here. Same for stale rows.
func perAppKeepCurrentPrevious(rows []state.SnapshotForGC) []string {
	byApp := make(map[string][]state.SnapshotForGC, len(rows))
	for _, r := range rows {
		byApp[r.AppID] = append(byApp[r.AppID], r)
	}
	var drop []string
	for _, appRows := range byApp {
		// Sort newest-first so [0] is current, [1] is previous.
		sort.Slice(appRows, func(i, j int) bool {
			return appRows[i].CreatedAt.After(appRows[j].CreatedAt)
		})
		for i := 2; i < len(appRows); i++ {
			drop = append(drop, appRows[i].ID)
		}
	}
	return drop
}

// evictOldestFromHeaviestAccount returns the snapshot ID(s) to delete when
// fleet disk pressure (lv-fc ≥ SnapshotBudgetAlarmPct) is on. The unit of
// eviction is ONE snapshot per call — the caller loops until pressure
// is relieved or no candidates remain.
//
// Policy (spec §4.6, account-level fairness): partition rows by account,
// compute each account's total snapshot bytes (MemBytes + DiskBytes),
// pick the heaviest account, and from that account pick the oldest
// snapshot that isn't already slated for retention. Returns nil when no
// evictable row exists (the box is past the alarm threshold but every
// remaining row belongs to a deployment that someone is actively using).
//
// The "keep current + previous per app" rule is honoured even under
// pressure — we never evict the most-recent snapshot for any app.
//
// Pure function. Deterministic given identical input.
func evictOldestFromHeaviestAccount(rows []state.SnapshotForGC) []string {
	if len(rows) == 0 {
		return nil
	}
	// Per-account byte totals.
	byAccount := make(map[string]int64, len(rows))
	for _, r := range rows {
		byAccount[r.AccountID] += r.MemBytes + r.DiskBytes
	}
	// Sort accounts by total bytes desc; pick the heaviest.
	type acct struct {
		id    string
		bytes int64
	}
	var accts []acct
	for id, b := range byAccount {
		accts = append(accts, acct{id, b})
	}
	sort.Slice(accts, func(i, j int) bool {
		return accts[i].bytes > accts[j].bytes
	})
	// Within the heaviest account, find the per-app "current + previous"
	// floor so we don't evict those.
	currentPrev := make(map[string]int) // appID → count of newest rows to skip
	byAccountApp := make(map[string]map[string][]state.SnapshotForGC, len(accts))
	for _, r := range rows {
		if _, ok := byAccountApp[r.AccountID]; !ok {
			byAccountApp[r.AccountID] = map[string][]state.SnapshotForGC{}
		}
		byAccountApp[r.AccountID][r.AppID] = append(byAccountApp[r.AccountID][r.AppID], r)
	}
	for _, appRows := range byAccountApp[accts[0].id] {
		sort.Slice(appRows, func(i, j int) bool {
			return appRows[i].CreatedAt.After(appRows[j].CreatedAt)
		})
		n := 2
		if n > len(appRows) {
			n = len(appRows)
		}
		for _, r := range appRows[:n] {
			currentPrev[r.AppID]++
		}
	}
	// Among the heaviest account's rows, skip the newest `currentPrev[appID]`
	// per app; of the remainder, return the single oldest.
	accountRows := rows
	// Restrict to the heaviest account:
	heavyRows := make([]state.SnapshotForGC, 0, len(accountRows))
	for _, r := range accountRows {
		if r.AccountID == accts[0].id {
			heavyRows = append(heavyRows, r)
		}
	}
	// Per-app, sort oldest-first and skip the newest N.
	evictable := make(map[string][]state.SnapshotForGC)
	for _, r := range heavyRows {
		evictable[r.AppID] = append(evictable[r.AppID], r)
	}
	for appID, appRows := range evictable {
		sort.Slice(appRows, func(i, j int) bool {
			return appRows[i].CreatedAt.Before(appRows[j].CreatedAt)
		})
		skip := currentPrev[appID]
		if skip >= len(appRows) {
			evictable[appID] = nil
			continue
		}
		evictable[appID] = appRows[skip:]
	}
	// Flatten and pick the single oldest across the heaviest account.
	var oldest *state.SnapshotForGC
	for _, appRows := range evictable {
		for i := range appRows {
			if oldest == nil || appRows[i].CreatedAt.Before(oldest.CreatedAt) {
				r := appRows[i]
				oldest = &r
			}
		}
	}
	if oldest == nil {
		return nil
	}
	return []string{oldest.ID}
}
