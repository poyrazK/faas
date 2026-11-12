package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	ownersql "github.com/onebox-faas/faas/pkg/state/resourceownership/sqlc"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OrgBillingEmailMutationStore keeps the provider change and local email save
// in one serialized mutation. The callback receives the current, provider-
// qualified customer and requested email. It must only perform provider I/O,
// without calling the store, and is never retried inside the mutation.
type OrgBillingEmailMutationStore interface {
	UpdateOrgBillingEmailWithSync(context.Context, string, string, string, func(context.Context, Account) error) error
}

func (s *PgStore) UpdateOrgBillingEmailWithSync(ctx context.Context, orgID, provider, email string, syncCustomer func(context.Context, Account) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackOrgAccess(ctx, tx)
	q, key := sqlc.New(), mustPgUUID(orgID)
	if _, err := q.LockBillingIdentityOrg(ctx, tx, key); err != nil {
		return mapErr(err)
	}
	orgs := ownersql.New()
	row, err := orgs.OrgAccount(ctx, tx, key)
	if err != nil {
		return mapErr(err)
	}
	account := orgResourceAccount(row)
	if provider != "" {
		account.ProviderCustomerID, account.StripeSubscriptionItem = "", ""
		identity, err := q.BillingIdentityForMutation(ctx, tx, sqlc.BillingIdentityForMutationParams{AccountID: key, Provider: provider})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return mapErr(err)
		}
		if err == nil {
			account.ProviderCustomerID, account.StripeSubscriptionItem = identity.CustomerID, identity.SubscriptionID
		}
	}
	account.Email = email
	if syncCustomer != nil {
		if err := syncCustomer(ctx, account); err != nil {
			return err
		}
	}
	if _, err := orgs.UpdateBillingEmail(ctx, tx, ownersql.UpdateBillingEmailParams{ID: key, BillingEmail: email}); err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

func (m *MemStore) UpdateOrgBillingEmailWithSync(ctx context.Context, orgID, provider, email string, syncCustomer func(context.Context, Account) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return err
	}
	org, exists := m.orgs[orgID]
	if !exists || org.Personal {
		return ErrNotFound
	}
	account := m.orgResourceAccountLocked(org)
	qualified := account
	if provider != "" {
		identity := m.billingIdentities[billingIdentityKey(orgID, provider)]
		qualified.ProviderCustomerID, qualified.StripeSubscriptionItem = identity.CustomerID, identity.SubscriptionID
	}
	qualified.Email = email
	if syncCustomer != nil {
		if err := syncCustomer(ctx, qualified); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	account.Email = email
	m.saveOrgResourcePolicyLocked(account)
	return nil
}
