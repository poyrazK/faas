package state

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// CronAccountQuotaStore reports which of a billing subject's crons may fire
// under its plan's per-app and per-account caps. The eligible set is ranked
// globally by age (created_at, then id), so every schedd node computes the
// SAME set: the per-account cap must hold across the fleet, not once per node.
// A node-local per-tick counter (the schedd cron sweep's previous behaviour)
// let an account whose apps span N nodes keep up to N x CronLimitPerAccount
// crons firing after a downgrade, because each node counted only its own owned
// crons.
type CronAccountQuotaStore interface {
	// BillingSubjectEligibleCronIDs returns the ids of the enabled,
	// non-suspended crons of subjectID's apps that fit within both caps: at
	// most perApp per app and at most perAccount in total, keeping the
	// oldest. The subject is an app's billing subject (ADR-202610171904, see
	// quotaSubjectSQL): a company once it adopted billing, whoever created
	// its apps, else the creator, whose personal and unadopted company apps
	// share one allowance. perApp<=0 or perAccount<=0 (a Free-tier 0 cap)
	// returns an empty slice.
	BillingSubjectEligibleCronIDs(ctx context.Context, subjectID string, perApp, perAccount int) ([]string, error)
}

// CronFireClaimStore gates the legacy policy-less HTTP cron dispatch with an
// optimistic compare-and-swap on last_fired_at. That path dispatches before it
// stamps the cursor; without this guard two schedds that briefly both own the
// app (a placement handoff) would each fire the same occurrence. Modern
// (schedule_policy) crons already get this guard inside
// CreateScheduledCron*Occurrence.
type CronFireClaimStore interface {
	// ClaimCronFire advances a cron's last_fired_at from expectedLastFired to
	// firedAt, returning true only when the current value still equals
	// expectedLastFired (a zero time matches a never-fired NULL cursor). A
	// false return means another writer advanced the cursor first.
	ClaimCronFire(ctx context.Context, cronID string, expectedLastFired, firedAt time.Time) (bool, error)
}

// BillingSubjectEligibleCronIDs implements CronAccountQuotaStore. The window
// function ranks each cron within its app by age; the outer query keeps those
// within the per-app cap and takes the oldest perAccount of them, so the
// result is node-independent. The account_id/org_id prefilter keeps the scan
// on apps_account_idx and apps_org_quota_idx.
func (s *PgStore) BillingSubjectEligibleCronIDs(ctx context.Context, subjectID string, perApp, perAccount int) ([]string, error) {
	if subjectID == "" || perApp <= 0 || perAccount <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		with ranked as (
		  select c.id, c.created_at,
		         row_number() over (partition by c.app_id order by c.created_at, c.id) as app_rank
		    from crons c
		    join apps a on a.id = c.app_id
		    left join orgs o on o.id = a.org_id
		   where (a.account_id = $1::uuid or a.org_id = $1::uuid)
		     and `+quotaSubjectSQL+` = $1::uuid
		     and a.status <> 'deleted'
		     and c.enabled
		     and c.suspended_reason = ''
		)
		select id from ranked
		 where app_rank <= $2
		 order by created_at, id
		 limit $3`, subjectID, perApp, perAccount)
	if err != nil {
		return nil, fmt.Errorf("state: billing subject eligible crons %s: %w", subjectID, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("state: scan eligible cron: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ClaimCronFire implements CronFireClaimStore. `is not distinct from` makes the
// compare match a NULL cursor when expectedLastFired is zero; passing a zero
// firedAt likewise clears the cursor (used to roll a claim back).
func (s *PgStore) ClaimCronFire(ctx context.Context, cronID string, expectedLastFired, firedAt time.Time) (bool, error) {
	if cronID == "" {
		return false, ErrInvalidArgument
	}
	var next, expected any
	if !firedAt.IsZero() {
		next = firedAt.UTC()
	}
	if !expectedLastFired.IsZero() {
		expected = expectedLastFired.UTC()
	}
	tag, err := s.pool.Exec(ctx,
		`update crons set last_fired_at = $2
		   where id = $1 and last_fired_at is not distinct from $3`,
		cronID, next, expected)
	if err != nil {
		return false, fmt.Errorf("state: claim cron fire %s: %w", cronID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// BillingSubjectEligibleCronIDs implements CronAccountQuotaStore for the
// in-memory store, mirroring the PgStore query shape.
func (m *MemStore) BillingSubjectEligibleCronIDs(_ context.Context, subjectID string, perApp, perAccount int) ([]string, error) {
	if subjectID == "" || perApp <= 0 || perAccount <= 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var cand []Cron
	for _, c := range m.crons {
		app, ok := m.apps[c.AppID]
		if !ok || !m.countsTowardQuotaSubjectLocked(app.AccountID, app.ID, subjectID) {
			continue
		}
		if !c.Enabled || c.SuspendedReason != "" {
			continue
		}
		cand = append(cand, c)
	}
	sort.Slice(cand, func(i, j int) bool {
		if !cand[i].CreatedAt.Equal(cand[j].CreatedAt) {
			return cand[i].CreatedAt.Before(cand[j].CreatedAt)
		}
		return cand[i].ID < cand[j].ID
	})
	appSeen := map[string]int{}
	ids := make([]string, 0, perAccount)
	for _, c := range cand {
		appSeen[c.AppID]++
		if appSeen[c.AppID] > perApp {
			continue
		}
		ids = append(ids, c.ID)
		if len(ids) == perAccount {
			break
		}
	}
	return ids, nil
}

// ClaimCronFire implements CronFireClaimStore for the in-memory store.
func (m *MemStore) ClaimCronFire(_ context.Context, cronID string, expectedLastFired, firedAt time.Time) (bool, error) {
	if cronID == "" {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.crons[cronID]
	if !ok {
		return false, ErrNotFound
	}
	if !c.LastFiredAt.Equal(expectedLastFired) {
		return false, nil
	}
	c.LastFiredAt = firedAt
	m.crons[cronID] = c
	return true, nil
}
