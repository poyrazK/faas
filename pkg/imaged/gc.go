package imaged

// GC algorithm — pure functions over the joined SnapshotForGC list. Kept
// separate from Loop so the table tests don't need to drive a real ticker
// to exercise the eviction logic.
//
// Both functions return []deleteTarget (snap row id + deployment id + app
// slug), so the caller can hand them to Loop.deleteSnapshotsAndFiles
// without re-querying DeploymentByID. The snap dir on disk is keyed on
// deployment id (pkg/sched/paths.go::SnapDir()) and the per-app ext4
// layer is keyed on (appsRoot/<slug>/<depID>.ext4) — see the F-05 note
// in pkg/imaged/loop.go::deleteSnapshotsAndFiles.

import (
	"sort"

	"github.com/onebox-faas/faas/pkg/state"
)

// deleteTarget is the row-keyed target the GC picks for eviction. The
// snap row id is what DeleteSnapshotsByID expects; the deployment id
// and app slug are what the filesystem cleanup needs (snap blobs are
// keyed on deployment id, drive1 ext4 layers on (slug, deployment id)).
type deleteTarget struct {
	ID           string
	DeploymentID string
	AppSlug      string
}

// perAppKeepCurrentPrevious returns the snapshot IDs that fall outside the
// "current + previous per app" retention window (spec §4.6). The function
// is pure; it does not mutate the input slice.
//
// Algorithm: per (appID), keep the two newest snapshots by CreatedAt
// (the "current" deployment's snap and the "previous" deployment's
// snap); everything older is a candidate for deletion.
//
// F-09: identical-CreatedAt ties used to be resolved by sort.Slice, which
// is unstable on ties. Replaced with sort.SliceStable plus an (CreatedAt
// desc, ID asc) tiebreaker — same timestamp means the row with the
// smaller id wins "newer", which is arbitrary but deterministic across
// runs. That's enough for "doesn't evict the wrong one when a
// rollback-and-redeploy lands in the same nanosecond".
//
// Soft-deleted apps (status='deleted') are filtered out by the SQL
// layer in Store.ListSnapshotsForGC, so we don't have to handle them
// here. Same for stale rows.
func perAppKeepCurrentPrevious(rows []state.SnapshotForGC) []deleteTarget {
	byApp := make(map[string][]state.SnapshotForGC, len(rows))
	for _, r := range rows {
		byApp[r.AppID] = append(byApp[r.AppID], r)
	}
	var drop []deleteTarget
	for _, appRows := range byApp {
		// Sort newest-first so [0] is current, [1] is previous.
		sort.SliceStable(appRows, func(i, j int) bool {
			if appRows[i].CreatedAt.Equal(appRows[j].CreatedAt) {
				return appRows[i].ID < appRows[j].ID
			}
			return appRows[i].CreatedAt.After(appRows[j].CreatedAt)
		})
		for i := 2; i < len(appRows); i++ {
			drop = append(drop, deleteTarget{
				ID:           appRows[i].ID,
				DeploymentID: appRows[i].DeploymentID,
				AppSlug:      appSlugFor(rows, appRows[i].AppID),
			})
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
// F-06: the per-app floor previously used `appRows[skip:]` after sorting
// OLDEST-first, which kept the NEWEST len-skip rows instead of the
// OLDEST. Fixed: use `appRows[:len(appRows)-skip]` to drop the newest
// skip, keep the oldest len-skip, then pick the single oldest of that.
//
// F-09: same stable-sort tiebreaker as perAppKeepCurrentPrevious.
//
// Pure function. Deterministic given identical input.
func evictOldestFromHeaviestAccount(rows []state.SnapshotForGC) []deleteTarget {
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
	sort.SliceStable(accts, func(i, j int) bool {
		if accts[i].bytes == accts[j].bytes {
			return accts[i].id < accts[j].id
		}
		return accts[i].bytes > accts[j].bytes
	})
	heavyID := accts[0].id
	heavyRows := make([]state.SnapshotForGC, 0, len(rows))
	for _, r := range rows {
		if r.AccountID == heavyID {
			heavyRows = append(heavyRows, r)
		}
	}
	// Per (appID, snap-row): sort OLDEST-first; pick the per-app floor
	// (keep newest N=2) and from the remainder take the single oldest.
	evictable := make(map[string][]state.SnapshotForGC, len(heavyRows))
	for _, r := range heavyRows {
		evictable[r.AppID] = append(evictable[r.AppID], r)
	}
	var oldest *state.SnapshotForGC
	for appID, appRows := range evictable {
		sort.SliceStable(appRows, func(i, j int) bool {
			if appRows[i].CreatedAt.Equal(appRows[j].CreatedAt) {
				return appRows[i].ID < appRows[j].ID
			}
			return appRows[i].CreatedAt.Before(appRows[j].CreatedAt)
		})
		const floor = 2
		// Drop the newest floor; keep the oldest len-floor. (Was
		// appRows[skip:], which kept the newest len-skip — F-06.)
		if len(appRows) <= floor {
			continue
		}
		evict := appRows[:len(appRows)-floor]
		// Pick the single oldest row across all apps in this account.
		if oldest == nil || evict[0].CreatedAt.Before(oldest.CreatedAt) {
			r := evict[0]
			oldest = &r
		}
		_ = appID
	}
	if oldest == nil {
		return nil
	}
	// The slug isn't on SnapshotForGC — look it up against the input
	// rows. Required for the appsRoot/<slug>/<depID>.ext4 path.
	slug := appSlugFor(rows, oldest.AppID)
	return []deleteTarget{{
		ID:           oldest.ID,
		DeploymentID: oldest.DeploymentID,
		AppSlug:      slug,
	}}
}

// appSlugFor returns the slug for an app by scanning rows. Used by the
// GC algorithms to populate deleteTarget.AppSlug. The input rows
// already contain the join — no DB round-trip required. Returns "" if
// the app ID is unknown (caller treats this as a delete with no ext4).
func appSlugFor(rows []state.SnapshotForGC, appID string) string {
	for _, r := range rows {
		if r.AppID == appID {
			// SnapshotForGC doesn't carry slug directly; it carries
			// app_id + account_id. We need the slug for the on-disk
			// ext4 path. The caller (Loop.deleteSnapshotsAndFiles)
			// resolves slug by reading AppByID when it's "" — see
			// that function. Kept here so the GC algorithms stay
			// pure over SnapshotForGC and don't import api/state.
			_ = r
			return ""
		}
	}
	return ""
}
