package state

import (
	"context"
	"sort"
	"time"
)

// BillingIdentityLister pages through one provider's billing identities in
// account order. apid's subscription reconciliation reads it
// (ADR-20261029094512).
type BillingIdentityLister interface {
	// ListBillingIdentities returns up to limit identities of provider whose
	// account ID sorts after afterAccountID ("" starts at the beginning).
	ListBillingIdentities(ctx context.Context, provider, afterAccountID string, limit int) ([]BillingIdentity, error)
}

// BillingReconcileLocker serialises subscription reconciliation passes
// across apid replicas. A store without it runs a single apid.
type BillingReconcileLocker interface {
	// TryAcquireBillingReconcileLock makes one non-blocking attempt;
	// acquired=false means another replica is running a pass.
	TryAcquireBillingReconcileLock(ctx context.Context) (release func(context.Context), acquired bool, err error)
}

var (
	_ BillingIdentityLister  = (*PgStore)(nil)
	_ BillingIdentityLister  = (*MemStore)(nil)
	_ BillingReconcileLocker = (*PgStore)(nil)
)

func (s *PgStore) ListBillingIdentities(ctx context.Context, provider, afterAccountID string, limit int) ([]BillingIdentity, error) {
	var after *string
	if afterAccountID != "" {
		after = &afterAccountID
	}
	rows, err := s.pool.Query(ctx,
		`select account_id, provider, customer_id, subscription_id, billing_from, created_at, updated_at
		   from billing_identities
		  where provider = $1 and ($2::uuid is null or account_id > $2::uuid)
		  order by account_id
		  limit $3`, provider, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var identities []BillingIdentity
	for rows.Next() {
		var identity BillingIdentity
		if err := rows.Scan(&identity.AccountID, &identity.Provider, &identity.CustomerID,
			&identity.SubscriptionID, &identity.BillingFrom, &identity.CreatedAt, &identity.UpdatedAt); err != nil {
			return nil, err
		}
		identities = append(identities, identity)
	}
	return identities, rows.Err()
}

func (s *PgStore) TryAcquireBillingReconcileLock(ctx context.Context) (func(context.Context), bool, error) {
	return s.tryAcquireSessionAdvisoryLock(ctx, sessionAdvisoryLock{
		what:      "billing subscription reconciliation",
		tryLock:   `select pg_try_advisory_lock(hashtextextended($1, 0))`,
		unlock:    `select pg_advisory_unlock(hashtextextended($1, 0))`,
		keyArg:    "billing-subscription-reconcile",
		retryWait: 50 * time.Millisecond,
	})
}

func (m *MemStore) ListBillingIdentities(_ context.Context, provider, afterAccountID string, limit int) ([]BillingIdentity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var identities []BillingIdentity
	for _, identity := range m.billingIdentities {
		if identity.Provider == provider && identity.AccountID > afterAccountID {
			identities = append(identities, identity)
		}
	}
	sort.Slice(identities, func(i, j int) bool { return identities[i].AccountID < identities[j].AccountID })
	if len(identities) > limit {
		identities = identities[:limit]
	}
	return identities, nil
}
