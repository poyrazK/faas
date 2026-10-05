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
	StorageKey          string
	ID                  string
	DeploymentID        string
	AppID               string
	AccountID           string
	AppSlug             string
	DeploymentStatus    state.DeploymentStatus
	DeploymentRootfsKey string
	Tier                string
}

// perAppKeepTierFloor (issue #470 / PR C / ADR-074) returns the
// snapshot IDs that fall outside the per-tier retention window.
// The function is pure; it does not mutate the input slice.
//
// Algorithm: per app and original environment lifetime, partition by tier.
// Each deployment uses its own pinned warm policy:
//
//   - enabled: keep the 2 newest warm-tier rows + the 2 newest
//     init-tier rows. Drop everything older in either tier.
//   - disabled: warm-tier rows from that deployment are vestigial and
//     dropped. Init rows still participate in the 2-row init floor.
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
		if snapshotCleanupDebt(r) {
			drop = append(drop, targetForSnapshot(r))
			continue
		}
		byApp[snapshotRetentionKey(r)] = append(byApp[snapshotRetentionKey(r)], r)
	}
	for _, appRows := range byApp {
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
			case r.Tier == state.SnapshotTierWarm && r.AppWarmSnapshotEnabled && warmKept < 2:
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
// Each environment lifetime has its own generation window. Warm-enabled
// deployments retain both tiers; warm-disabled deployments retain only init.
// Vestigial warm rows
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
		if snapshotCleanupDebt(r) {
			drop = append(drop, targetForSnapshot(r))
			continue
		}
		byDeployment := byApp[snapshotRetentionKey(r)]
		if byDeployment == nil {
			byDeployment = make(map[string]*deploymentGroup)
			byApp[snapshotRetentionKey(r)] = byDeployment
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
		ID:                  r.ID,
		DeploymentID:        r.DeploymentID,
		AppID:               r.AppID,
		AccountID:           r.AccountID,
		StorageKey:          r.StorageKey,
		AppSlug:             r.AppSlug,
		DeploymentStatus:    r.DeploymentStatus,
		DeploymentRootfsKey: r.DeploymentRootfsKey,
		Tier:                r.Tier,
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
	candidates := perAppRollbackEvictionCandidates(heavyRows, api.SnapshotRollbackRetentionDeployments)
	if len(candidates) == 0 {
		return nil
	}
	return []deleteTarget{targetForSnapshot(candidates[0])}
}

// Pressure GC uses exactly the same environment and generation protection as
// the regular sweep. Account byte totals still account for all stages together.
func perAppRollbackEvictionCandidates(rows []state.SnapshotForGC, keepDeployments int) []state.SnapshotForGC {
	drops := perAppKeepRollbackWindow(rows, keepDeployments)
	eligible := make(map[string]struct{}, len(drops))
	for _, target := range drops {
		eligible[target.ID] = struct{}{}
	}
	candidates := make([]state.SnapshotForGC, 0, len(drops))
	for _, row := range rows {
		if _, ok := eligible[row.ID]; ok {
			candidates = append(candidates, row)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].CreatedAt.Equal(candidates[j].CreatedAt) {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
	})
	return candidates
}

// An environment UUID distinguishes deleted/recreated stages with the same
// slug. Legacy default and production share one retention window.
func snapshotRetentionKey(row state.SnapshotForGC) string {
	owner := row.EnvironmentID
	if owner == "" {
		scope := row.Scope
		if scope == "" || scope == "default" {
			scope = "production"
		}
		owner = "scope:" + scope
	} else {
		owner = "environment:" + owner
	}
	return row.AppID + ":" + owner
}

func snapshotCleanupDebt(row state.SnapshotForGC) bool {
	return row.RuntimeOwnerInvalid || row.AppStatus == state.AppDeleted ||
		row.DeploymentStatus == state.DeployFailed || row.DeploymentStatus == state.DeployCancelled
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
