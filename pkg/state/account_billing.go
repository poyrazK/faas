package state

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AccountBillingInfoPatch distinguishes omitted fields from explicit clears.
type AccountBillingInfoPatch struct {
	BusinessName, BillingAddress, TaxID *string
}

type AccountBillingInfoPatchStore interface {
	PatchAccountBillingInfo(context.Context, string, AccountBillingInfoPatch) (Account, error)
}

func billingPatchText(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func (s *PgStore) PatchAccountBillingInfo(ctx context.Context, id string, patch AccountBillingInfoPatch) (Account, error) {
	q := sqlc.New()
	params := sqlc.PatchOrgBillingInfoParams{ID: mustPgUUID(id), BusinessName: billingPatchText(patch.BusinessName), BillingAddress: billingPatchText(patch.BillingAddress), TaxID: billingPatchText(patch.TaxID)}
	if n, err := q.PatchOrgBillingInfo(ctx, s.pool, params); err != nil {
		return Account{}, mapErr(err)
	} else if n > 0 {
		return s.OrgResourceAccount(ctx, id)
	}
	n, err := q.PatchPersonalAccountBillingInfo(ctx, s.pool, sqlc.PatchPersonalAccountBillingInfoParams(params))
	if err != nil {
		return Account{}, mapErr(err)
	}
	if n == 0 {
		return Account{}, ErrNotFound
	}
	return s.AccountByID(ctx, id)
}

func (m *MemStore) PatchAccountBillingInfo(ctx context.Context, id string, patch AccountBillingInfoPatch) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return Account{}, err
	}
	org, shared := m.orgs[id]
	shared = shared && !org.Personal
	a, exists := m.accounts[id]
	if shared {
		a, exists = m.orgResourceAccountLocked(org), true
	}
	if !exists {
		return Account{}, ErrNotFound
	}
	if patch.BusinessName != nil {
		a.BusinessName = *patch.BusinessName
	}
	if patch.BillingAddress != nil {
		a.BillingAddress = *patch.BillingAddress
	}
	if patch.TaxID != nil {
		a.TaxID = *patch.TaxID
	}
	if shared {
		m.saveOrgResourcePolicyLocked(a)
	} else {
		m.storeRouteCheckAccountLocked(id, a)
	}
	return a, nil
}
