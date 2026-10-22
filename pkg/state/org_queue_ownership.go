package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// queuePolicyAccountDB keeps immutable namespaces separate from current policy.
// Company policy locks precede environment, app, invocation and quota locks.
func queuePolicyAccountDB(ctx context.Context, db sqlc.DBTX, accountID, appID string, lock bool) (Account, error) {
	q := sqlc.New()
	account, app := mustPgUUID(accountID), mustPgUUID(appID)
	if lock {
		if err := q.OrgQueueLockPolicy(ctx, db, sqlc.OrgQueueLockPolicyParams{AppID: app, AccountID: account}); err != nil {
			return Account{}, mapErr(err)
		}
	}
	row, err := q.OrgQueueReadPolicy(ctx, db, sqlc.OrgQueueReadPolicyParams{AppID: app, AccountID: account})
	if err != nil {
		return Account{}, mapErr(err)
	}
	owner := Account{ID: accountID, Plan: api.Plan(row.Plan), Status: AccountStatus(row.Status), ResourceOrgID: pgUUIDString(row.OwnerOrgID)}
	owner.ResourceOwnershipEnabled = owner.ResourceOrgID != ""
	owner.AsyncQuotaPooled = owner.ResourceOwnershipEnabled && row.AsyncQuotaPooled
	if row.AbuseHoldAt.Valid {
		owner.AbuseHoldAt = &row.AbuseHoldAt.Time
	}
	return owner, nil
}

func (m *MemStore) queuePolicyAccountLocked(accountID, appID string) (Account, bool) {
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID {
		return Account{}, false
	}
	return m.appResourceAccountLocked(app)
}

func invocationAsyncSubject(inv Invocation) string {
	if inv.AsyncQuotaSubjectID != "" {
		return inv.AsyncQuotaSubjectID
	}
	return inv.AccountID
}

func (m *MemStore) queueQuotaSubjectLocked(inv Invocation, fallbackMax int) (string, int, error) {
	owner, ok := m.queuePolicyAccountLocked(inv.AccountID, inv.AppID)
	if !ok {
		return "", 0, ErrNotFound
	}
	if owner.ResourceOwnershipEnabled && !owner.Active() {
		return "", 0, ErrConflict
	}
	if owner.AsyncQuotaPooled {
		return owner.AsyncQuotaSubjectID(), api.MustLimitsFor(owner.Plan).MaxAsyncInvocationsPerAccount, nil
	}
	return inv.AccountID, fallbackMax, nil
}

// poolOrgAsyncReservationsLocked mirrors adopt_org_async_reservations: the
// organization's later reservations use its own counter, and its open ones
// move there. No release calls it yet: the previous release decrements the
// namespace counter, so pooling waits for the release after this one
// (ADR-202610171908).
func (m *MemStore) poolOrgAsyncReservationsLocked(orgID string) error {
	org, ok := m.orgs[orgID]
	if !ok || org.Personal || !org.ResourceOwnershipEnabled {
		return nil
	}
	if err := m.validateQueueReservationsForAdoptionLocked(org); err != nil {
		return err
	}
	if m.orgAsyncQuotaPooled == nil {
		m.orgAsyncQuotaPooled = make(map[string]time.Time)
	}
	if _, pooled := m.orgAsyncQuotaPooled[org.ID]; !pooled {
		m.orgAsyncQuotaPooled[org.ID] = m.clock().UTC()
	}
	m.adoptQueueReservationsLocked(org)
	return nil
}

// Company adoption moves only invocation contributions; exclusive jobs and
// personal reservations on the old counter keep their original ownership.
func (m *MemStore) adoptQueueReservationsLocked(org Org) {
	for id, inv := range m.invocations {
		if !inv.QuotaReserved || m.apps[inv.AppID].OrgID != org.ID || invocationAsyncSubject(inv) == org.ID {
			continue
		}
		m.decrementAccountAsyncInflightLocked(invocationAsyncSubject(inv))
		quota := m.accountAsyncQuota[org.ID]
		quota.MaxInflight = api.MustLimitsFor(org.Plan).MaxAsyncInvocationsPerAccount
		quota.CurrentInflight++
		m.accountAsyncQuota[org.ID] = quota
		inv.AsyncQuotaSubjectID = org.ID
		m.invocations[id] = inv
	}
}

func (m *MemStore) validateQueueReservationsForAdoptionLocked(org Org) error {
	counts := make(map[string]int)
	for _, inv := range m.invocations {
		if inv.QuotaReserved && m.apps[inv.AppID].OrgID == org.ID && invocationAsyncSubject(inv) != org.ID {
			counts[invocationAsyncSubject(inv)]++
		}
	}
	for subject, count := range counts {
		if m.accountAsyncQuota[subject].CurrentInflight < count {
			return ErrConflict
		}
	}
	return nil
}

func (m *MemStore) sameQueueQuotaSubjectLocked(app App, subject string) bool {
	return m.appBillingSubjectLocked(app.AccountID, app.ID) == subject
}

func (s *PgStore) PersonalQueueReference(ctx context.Context, accountID, kind, id string) (bool, error) {
	parsed, err := parsePgUUID(id)
	if err != nil {
		return false, nil //nolint:nilerr // Malformed historical references are excluded from exports.
	}
	account, err := parsePgUUID(accountID)
	if err != nil {
		return false, err
	}
	if kind == "queue_binding_id" {
		return sqlc.New().OrgQueuePersonalBindingReference(ctx, s.pool, sqlc.OrgQueuePersonalBindingReferenceParams{ID: parsed, AccountID: account})
	}
	return sqlc.New().OrgQueuePersonalInvocationReference(ctx, s.pool, sqlc.OrgQueuePersonalInvocationReferenceParams{ID: parsed, AccountID: account})
}

func (m *MemStore) PersonalQueueReference(ctx context.Context, accountID, kind, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return false, err
	}
	appID := m.invocations[id].AppID
	if kind == "queue_binding_id" {
		appID = m.queueBindings[id].AppID
	}
	app, ok := m.apps[appID]
	return ok && app.AccountID == accountID && m.personalAppLocked(app), nil
}

func ensureQueueAsyncQuotaDB(ctx context.Context, db sqlc.DBTX, owner Account) error {
	_, err := sqlc.New().OrgQueueEnsureQuota(ctx, db, sqlc.OrgQueueEnsureQuotaParams{
		AccountID: mustPgUUID(owner.AsyncQuotaSubjectID()), MaxInflight: int32(api.MustLimitsFor(owner.Plan).MaxAsyncInvocationsPerAccount)})
	return mapErr(err)
}

func lockQueueProjectPolicy(ctx context.Context, db sqlc.DBTX, accountID, projectID string) error {
	return mapErr(sqlc.New().OrgQueueLockProjectPolicy(ctx, db, sqlc.OrgQueueLockProjectPolicyParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID)}))
}

type OrgQueueUsageStore interface {
	OrgQueueUsage(context.Context, string) (api.OrgQueueUsageResponse, error)
}

func (s *PgStore) OrgQueueUsage(ctx context.Context, orgID string) (api.OrgQueueUsageResponse, error) {
	row, err := sqlc.New().OrgQueueUsage(ctx, s.pool, mustPgUUID(orgID))
	if err != nil {
		return api.OrgQueueUsageResponse{}, mapErr(err)
	}
	return api.OrgQueueUsageResponse{OrganizationID: orgID, Bindings: row.Bindings, Pending: row.Pending, Processing: row.Processing, DeadLetter: row.DeadLetter, ReservedAsync: row.ReservedAsync}, nil
}

func (m *MemStore) OrgQueueUsage(ctx context.Context, orgID string) (api.OrgQueueUsageResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.OrgQueueUsageResponse{}, err
	}
	out := api.OrgQueueUsageResponse{OrganizationID: orgID}
	for _, b := range m.queueBindings {
		if b.OrgID == orgID && b.RetiredAt == nil {
			out.Bindings++
		}
	}
	for _, inv := range m.invocations {
		if m.apps[inv.AppID].OrgID != orgID {
			continue
		}
		if inv.QuotaReserved {
			out.ReservedAsync++
		}
		if inv.Source != InvocationQueue {
			continue
		}
		switch inv.State {
		case InvocationPending:
			out.Pending++
		case InvocationDispatching:
			out.Processing++
		case InvocationDeadLetter:
			out.DeadLetter++
		}
	}
	return out, nil
}
