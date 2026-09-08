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
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// deleteTarget is the row-keyed target the GC picks for eviction. The
// snap row id is what DeleteSnapshotsByID expects; the deployment id
// and app slug are what the filesystem cleanup needs (snap blobs are
// keyed on deployment id, drive1 ext4 layers on (slug, deployment id)).
//
// Tier (issue #470 / PR C / ADR-074) drives the storage key the filesystem
// cleanup uses: warm-tier targets delete WarmSnapMemKey + WarmSnapVMStateKey;
// init-tier targets delete SnapMemKey + SnapVMStateKey. The shared per-app
// ext4 is deleted only after the deployment has no remaining snapshot tier.
type deleteTarget struct {
	StorageKey   string
	ID           string
	DeploymentID string
	AppSlug      string
	Tier         string
}

// perAppKeepTierFloor (issue #470 / PR C / ADR-074) returns the
// snapshot IDs that fall outside the per-tier retention window.
// The function is pure; it does not mutate the input slice.
//
// Algorithm: per (appID), partition rows by tier. For each app, the
// floor depends on apps.warm_snapshot_enabled:
//
//   - enabled: keep the 2 newest warm-tier rows + the 2 newest
//     init-tier rows. Drop everything older in either tier.
//   - disabled: keep the 2 newest init-tier rows only. Drop every
//     warm-tier row (the app is not opted in to warm; those rows
//     are vestigial from a previous opt-in) plus the older init rows.
//
// F-09: identical-CreatedAt ties used to be resolved by sort.Slice,
// which is unstable on ties. Replaced with sort.SliceStable plus an
// (CreatedAt desc, ID asc) tiebreaker — same timestamp means the
// row with the smaller id wins "newer", which is arbitrary but
// deterministic across runs. That's enough for "doesn't evict the
// wrong one when a rollback-and-redeploy lands in the same
// nanosecond".
//
// Deleted apps and unusable terminal deployments are returned by the store
// specifically so this function can evict them without letting them consume
// a retention-floor slot, even when lifecycle triggers already marked those
// rows stale. Other stale rows are handled by the retention sweep.
//
// Replaces the legacy perAppKeepCurrentPrevious (spec §4.6 current +
// previous) which ignored tier entirely.
func perAppKeepTierFloor(rows []state.SnapshotForGC) []deleteTarget {
	byApp := make(map[string][]state.SnapshotForGC, len(rows))
	var drop []deleteTarget
	for _, r := range rows {
		if r.AppStatus == state.AppDeleted ||
			r.DeploymentStatus == state.DeployFailed ||
			r.DeploymentStatus == state.DeployCancelled {
			drop = append(drop, targetForSnapshot(r))
			continue
		}
		byApp[r.AppID] = append(byApp[r.AppID], r)
	}
	for _, appRows := range byApp {
		// Pick the warm-tier policy from the first row's
		// AppWarmSnapshotEnabled (denormalised into the JOIN
		// projection; same flag for every row of an app).
		warmEnabled := len(appRows) > 0 && appRows[0].AppWarmSnapshotEnabled
		// Sort newest-first.
		sort.SliceStable(appRows, func(i, j int) bool {
			if appRows[i].CreatedAt.Equal(appRows[j].CreatedAt) {
				return appRows[i].ID < appRows[j].ID
			}
			return appRows[i].CreatedAt.After(appRows[j].CreatedAt)
		})
		var warmKept, initKept int
		for _, r := range appRows {
			switch {
			case r.Tier == state.SnapshotTierWarm && warmEnabled && warmKept < 2:
				warmKept++
				continue
			case r.Tier == state.SnapshotTierInit && initKept < 2:
				initKept++
				continue
			default:
				drop = append(drop, targetForSnapshot(r))
			}
		}
	}
	return drop
}

// perAppKeepRollbackWindow returns the snapshot rows that fall outside the
// bounded rollback window. Unlike the legacy per-tier floor above, this policy
// protects deployment generations rather than an arbitrary number of rows per
// tier: the newest keepDeployments deployments keep their non-stale snapshots
// so an operator rollback can restore the target without a cold boot.
//
// Warm-enabled apps retain both the warm and init snapshot for each protected
// deployment. Warm-disabled apps retain only init rows; vestigial warm rows
// are still eligible for cleanup. The input is never mutated and output order
// is deterministic for equal timestamps.
func perAppKeepRollbackWindow(rows []state.SnapshotForGC, keepDeployments int) []deleteTarget {
	if len(rows) == 0 {
		return nil
	}
	if keepDeployments < 1 {
		keepDeployments = 1
	}
	type deploymentGroup struct {
		id      string
		rows    []state.SnapshotForGC
		newest  state.SnapshotForGC
		warmSet bool
	}
	byApp := make(map[string]map[string]*deploymentGroup, len(rows))
	var drop []deleteTarget
	for _, r := range rows {
		// Lifecycle-triggered stale rows for deleted apps and failed/cancelled
		// deployments are included in the projection specifically so their
		// storage can be reclaimed immediately. They must not consume a
		// rollback slot for a healthy deployment generation.
		if r.AppStatus == state.AppDeleted ||
			r.DeploymentStatus == state.DeployFailed ||
			r.DeploymentStatus == state.DeployCancelled {
			drop = append(drop, targetForSnapshot(r))
			continue
		}
		byDeployment := byApp[r.AppID]
		if byDeployment == nil {
			byDeployment = make(map[string]*deploymentGroup)
			byApp[r.AppID] = byDeployment
		}
		group := byDeployment[r.DeploymentID]
		if group == nil {
			group = &deploymentGroup{id: r.DeploymentID, newest: r, warmSet: r.AppWarmSnapshotEnabled}
			byDeployment[r.DeploymentID] = group
		}
		group.rows = append(group.rows, r)
		if r.CreatedAt.After(group.newest.CreatedAt) ||
			(r.CreatedAt.Equal(group.newest.CreatedAt) && r.ID < group.newest.ID) {
			group.newest = r
		}
	}

	for _, byDeployment := range byApp {
		groups := make([]*deploymentGroup, 0, len(byDeployment))
		for _, group := range byDeployment {
			groups = append(groups, group)
		}
		sort.SliceStable(groups, func(i, j int) bool {
			if groups[i].newest.CreatedAt.Equal(groups[j].newest.CreatedAt) {
				return groups[i].id < groups[j].id
			}
			return groups[i].newest.CreatedAt.After(groups[j].newest.CreatedAt)
		})
		for index, group := range groups {
			sort.SliceStable(group.rows, func(i, j int) bool {
				if group.rows[i].CreatedAt.Equal(group.rows[j].CreatedAt) {
					if group.rows[i].Tier == group.rows[j].Tier {
						return group.rows[i].ID < group.rows[j].ID
					}
					return group.rows[i].Tier < group.rows[j].Tier
				}
				return group.rows[i].CreatedAt.After(group.rows[j].CreatedAt)
			})
			for _, r := range group.rows {
				protected := index < keepDeployments &&
					(group.warmSet || r.Tier != state.SnapshotTierWarm)
				if protected {
					continue
				}
				drop = append(drop, targetForSnapshot(r))
			}
		}
	}
	// App groups are accumulated through a map, so normalize the result before
	// handing it to the bulk deleter. The filesystem work is order-independent,
	// but deterministic targets make retries and diagnostics reproducible.
	sort.SliceStable(drop, func(i, j int) bool {
		if drop[i].DeploymentID != drop[j].DeploymentID {
			return drop[i].DeploymentID < drop[j].DeploymentID
		}
		if drop[i].ID != drop[j].ID {
			return drop[i].ID < drop[j].ID
		}
		return drop[i].Tier < drop[j].Tier
	})
	return drop
}

func targetForSnapshot(r state.SnapshotForGC) deleteTarget {
	return deleteTarget{
		ID:           r.ID,
		DeploymentID: r.DeploymentID,
		StorageKey:   r.StorageKey,
		AppSlug:      r.AppSlug,
		Tier:         r.Tier,
	}
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
// The rollback retention window is honoured even under pressure — we do not
// evict a snapshot for a protected deployment unless the policy has no older
// candidate left.
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
	// Per app, keep the newest rollback window of deployment generations and
	// from the remainder take the single oldest snapshot. Warm-enabled apps
	// keep both tiers for every protected deployment; warm-disabled apps keep
	// only init rows.
	evictable := make(map[string][]state.SnapshotForGC, len(heavyRows))
	for _, r := range heavyRows {
		evictable[r.AppID] = append(evictable[r.AppID], r)
	}
	var oldest *state.SnapshotForGC
	var oldestTier string
	for appID, appRows := range evictable {
		sort.SliceStable(appRows, func(i, j int) bool {
			if appRows[i].CreatedAt.Equal(appRows[j].CreatedAt) {
				return appRows[i].ID < appRows[j].ID
			}
			return appRows[i].CreatedAt.Before(appRows[j].CreatedAt)
		})
		warmEnabled := len(appRows) > 0 && appRows[0].AppWarmSnapshotEnabled
		// Build the eviction candidate pool by deployment-generation ranking.
		candidates := perAppRollbackEvictionCandidates(appRows, warmEnabled,
			api.SnapshotRollbackRetentionDeployments)
		if len(candidates) == 0 {
			continue
		}
		cand := candidates[0] // already oldest-first
		if oldest == nil || cand.CreatedAt.Before(oldest.CreatedAt) ||
			(cand.CreatedAt.Equal(oldest.CreatedAt) && cand.ID < oldest.ID) {
			r := cand
			oldest = &r
			oldestTier = cand.Tier
		}
		_ = appID
	}
	if oldest == nil {
		return nil
	}
	// B1.1: AppSlug is on SnapshotForGC (issue #195); no lookup
	// against the input rows required.
	return []deleteTarget{{
		ID:           oldest.ID,
		DeploymentID: oldest.DeploymentID,
		StorageKey:   oldest.StorageKey,
		AppSlug:      oldest.AppSlug,
		Tier:         oldestTier,
	}}
}

// perAppRollbackEvictionCandidates returns snapshots that are outside the
// protected deployment-generation window, sorted oldest-first. It is the
// pressure-GC counterpart to perAppKeepRollbackWindow and deliberately keeps
// the same warm-disabled cleanup rule.
func perAppRollbackEvictionCandidates(appRows []state.SnapshotForGC, warmEnabled bool, keepDeployments int) []state.SnapshotForGC {
	if keepDeployments < 1 {
		keepDeployments = 1
	}
	type deploymentGroup struct {
		id     string
		rows   []state.SnapshotForGC
		newest time.Time
	}
	byDeployment := make(map[string]*deploymentGroup, len(appRows))
	for _, r := range appRows {
		group := byDeployment[r.DeploymentID]
		if group == nil {
			group = &deploymentGroup{id: r.DeploymentID, newest: r.CreatedAt}
			byDeployment[r.DeploymentID] = group
		}
		group.rows = append(group.rows, r)
		if r.CreatedAt.After(group.newest) {
			group.newest = r.CreatedAt
		}
	}
	groups := make([]*deploymentGroup, 0, len(byDeployment))
	for _, group := range byDeployment {
		groups = append(groups, group)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].newest.Equal(groups[j].newest) {
			return groups[i].id < groups[j].id
		}
		return groups[i].newest.After(groups[j].newest)
	})
	protected := make(map[string]struct{}, keepDeployments)
	for i, group := range groups {
		if i >= keepDeployments {
			break
		}
		protected[group.id] = struct{}{}
	}

	candidates := make([]state.SnapshotForGC, 0, len(appRows))
	for _, r := range appRows {
		// Terminal rows are cleanup debt, not rollback material. They remain
		// evictable even when their deployment ID happens to fall inside the
		// newest-generation window.
		if r.AppStatus == state.AppDeleted ||
			r.DeploymentStatus == state.DeployFailed ||
			r.DeploymentStatus == state.DeployCancelled {
			candidates = append(candidates, r)
			continue
		}
		if _, ok := protected[r.DeploymentID]; ok && (warmEnabled || r.Tier != state.SnapshotTierWarm) {
			continue
		}
		candidates = append(candidates, r)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].CreatedAt.Equal(candidates[j].CreatedAt) {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
	})
	return candidates
}

// perAppEvictionCandidates (issue #470 / PR C / ADR-074) ranks the
// snapshots of a single app by eviction eligibility under per-tier
// floors. Sorted oldest-first so the caller can take candidates[0] for
// the single-eviction path. The slice carries only rows that are
// eligible for eviction (NOT within the per-tier floor of their app).
//
//   - warm_enabled=true: floor per tier is 2; warm and init rows are
//     pooled together and ranked by CreatedAt; either tier can be
//     evicted as long as the per-tier floor of 2 stays.
//   - warm_enabled=false: floor per tier is 2 (init only); no warm
//     rows are ever produced, but if any vestigial warm rows exist
//     from a prior opt-in, they are evicted first (strictly before
//     the init-tier floor).
func perAppEvictionCandidates(appRows []state.SnapshotForGC, warmEnabled bool) []state.SnapshotForGC {
	// Partition by tier.
	warm := make([]state.SnapshotForGC, 0, len(appRows))
	init := make([]state.SnapshotForGC, 0, len(appRows))
	for _, r := range appRows {
		switch r.Tier {
		case state.SnapshotTierWarm:
			warm = append(warm, r)
		default:
			init = append(init, r)
		}
	}
	sort.SliceStable(warm, func(i, j int) bool {
		if warm[i].CreatedAt.Equal(warm[j].CreatedAt) {
			return warm[i].ID < warm[j].ID
		}
		return warm[i].CreatedAt.Before(warm[j].CreatedAt)
	})
	sort.SliceStable(init, func(i, j int) bool {
		if init[i].CreatedAt.Equal(init[j].CreatedAt) {
			return init[i].ID < init[j].ID
		}
		return init[i].CreatedAt.Before(init[j].CreatedAt)
	})
	// Drop the 2 newest per tier; the rest is evictable.
	var out []state.SnapshotForGC
	if warmEnabled {
		if len(warm) > 2 {
			out = append(out, warm[:len(warm)-2]...)
		}
	} else {
		// Not opted in: every warm row is evictable (vestigial).
		out = append(out, warm...)
	}
	if len(init) > 2 {
		out = append(out, init[:len(init)-2]...)
	}
	// Final ranking: oldest-first across the candidate pool.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}
