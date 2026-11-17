package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// OverageCapsReader reads the overage caps of many accounts or organizations
// in one query (ADR 20261103164725). schedd's reaper checks every account
// with apps on its node each tick; most have no cap and need no month total.
type OverageCapsReader interface {
	// AccountOverageCaps returns the cap of each ID among ids that has one,
	// read as GetAccountOverageCapCents reads it. An ID without a cap, or
	// with no row, is absent.
	AccountOverageCaps(ctx context.Context, ids []string) (map[string]int64, error)
}

var (
	_ OverageCapsReader = (*PgStore)(nil)
	_ OverageCapsReader = (*MemStore)(nil)
)

// AccountOverageCaps reads accounts' caps, and organizations' caps for IDs
// that name a non-personal organization, like GetAccountOverageCapCents.
func (s *PgStore) AccountOverageCaps(ctx context.Context, ids []string) (map[string]int64, error) {
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if validUUID(id) {
			valid = append(valid, id)
		}
	}
	out := make(map[string]int64)
	if len(valid) == 0 {
		return out, nil
	}
	// A paid subject without a cap is held to its plan's self-service
	// ceiling (ADR-20261105120000), so its row is read too.
	rows, err := s.pool.Query(ctx, `
		select a.id::text, a.overage_cap_cents, a.plan
		  from accounts a
		 where a.id = any($1::uuid[]) and (a.overage_cap_cents is not null or a.plan <> 'free')
		union all
		select o.id::text, o.overage_cap_cents, o.plan
		  from orgs o
		 where o.id = any($1::uuid[]) and not o.personal_org and (o.overage_cap_cents is not null or o.plan <> 'free')
		   and not exists (select 1 from accounts a where a.id = o.id)`, valid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, plan string
		var stored *int64
		if err := rows.Scan(&id, &stored, &plan); err != nil {
			return nil, err
		}
		cents, ok := EffectiveOverageCapCents(api.Plan(plan), stored)
		if !ok {
			continue
		}
		out[id] = cents
	}
	return out, rows.Err()
}

// AccountOverageCaps mirrors PgStore.AccountOverageCaps.
func (m *MemStore) AccountOverageCaps(ctx context.Context, ids []string) (map[string]int64, error) {
	out := make(map[string]int64)
	for _, id := range ids {
		cents, ok, err := m.GetAccountOverageCapCents(ctx, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out[id] = cents
		}
	}
	return out, nil
}
