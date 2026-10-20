package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) orgResourceAccountLocked(org Org) Account {
	out := m.orgResourcePolicies[org.ID]
	base := organizationAccount(org)
	out.ID, out.ResourceOrgID, out.Plan, out.Status = base.ID, base.ResourceOrgID, base.Plan, base.Status
	out.ResourceOwnershipEnabled = org.ResourceOwnershipEnabled
	out.AbuseHoldAt = org.AbuseHoldAt
	out.AbuseHoldReason = org.AbuseHoldReason
	out.ProviderCustomerID, out.StripeSubscriptionItem, out.CreatedAt = base.ProviderCustomerID, base.StripeSubscriptionItem, base.CreatedAt
	return out
}

// appResourceAccountLocked is the mutex-held equivalent of AppResourceAccount;
// resource policy must never depend on an erased creator's account row.
func (m *MemStore) appResourceAccountLocked(app App) (Account, bool) {
	org, exists := m.orgs[app.OrgID]
	if exists && !org.Personal && org.ResourceOwnershipEnabled {
		owner := m.orgResourceAccountLocked(org)
		owner.ID = app.AccountID
		_, owner.AsyncQuotaPooled = m.orgAsyncQuotaPooled[org.ID]
		return owner, true
	}
	owner, ok := m.accounts[app.AccountID]
	if ok && exists && !org.Personal {
		if org.DeletedPending {
			owner.Status = AccountDeletedPending
		} else if org.Status != OrgStatusActive && org.Status != OrgStatusPastDue {
			owner.Status = AccountStatus(org.Status)
		}
		if org.AbuseHoldAt != nil {
			owner.AbuseHoldAt, owner.AbuseHoldReason = org.AbuseHoldAt, org.AbuseHoldReason
		}
	}
	return owner, ok
}

func (m *MemStore) OrgResourceAccount(_ context.Context, orgID string) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	org, ok := m.orgs[orgID]
	if !ok || org.Personal {
		return Account{}, ErrNotFound
	}
	return m.orgResourceAccountLocked(org), nil
}

func (m *MemStore) ListOrgResourceAccounts(_ context.Context) ([]Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Account, 0)
	for _, org := range m.orgs {
		if !org.Personal {
			out = append(out, m.orgResourceAccountLocked(org))
		}
	}
	return out, nil
}

func (m *MemStore) saveOrgResourcePolicyLocked(account Account) {
	if m.orgResourcePolicies == nil {
		m.orgResourcePolicies = make(map[string]Account)
	}
	m.orgResourcePolicies[account.ResourceOrgID] = account
}

func (m *MemStore) UpdateOrgBillingEmail(_ context.Context, orgID, email string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	org, ok := m.orgs[orgID]
	if !ok || org.Personal {
		return ErrNotFound
	}
	account := m.orgResourceAccountLocked(org)
	account.Email = email
	m.saveOrgResourcePolicyLocked(account)
	return nil
}

func (m *MemStore) appBillingSubjectLocked(accountID, appID string) string {
	app, ok := m.apps[appID]
	if !ok {
		return accountID
	}
	org, ok := m.orgs[app.OrgID]
	if ok && !org.Personal && org.ResourceOwnershipEnabled {
		return org.ID
	}
	return accountID
}

func (m *MemStore) personalAppLocked(app App) bool {
	return app.OrgID == "" || m.orgs[app.OrgID].Personal
}

func (m *MemStore) accountOwnershipErasureBlockedLocked(accountID string) bool {
	for _, org := range m.orgs {
		member := m.memberships[orgAccountKey{OrgID: org.ID, AccountID: accountID}]
		if !org.Personal && !org.DeletedPending && member.Role == OrgRoleOwner && member.RemovedAt == nil {
			return true
		}
	}
	shared := false
	for _, app := range m.apps {
		if app.AccountID != accountID || m.personalAppLocked(app) {
			continue
		}
		shared = true
		if _, exists := m.routeMonitorConfigs[app.ID]; exists {
			return true
		}
		if m.githubBindings[app.ID].InstallID != 0 || !m.orgs[app.OrgID].ResourceOwnershipEnabled {
			return true
		}
	}
	for _, project := range m.projects {
		org := m.orgs[project.OrgID]
		if project.AccountID != accountID || project.OrgID == "" || org.Personal {
			continue
		}
		shared = true
		if project.InstallID != 0 || !org.ResourceOwnershipEnabled {
			return true
		}
		for _, source := range m.environmentGitOps {
			if source.source.ProjectID == project.ID {
				return true
			}
		}
	}
	if !shared {
		return false
	}
	// These stores are outside the core app lifecycle. PostgreSQL additionally
	// checks the complete migration inventory of identity-bound child tables.
	for _, r := range m.objectBuckets {
		if r.AccountID == accountID && !m.personalObjectBucketLocked(r) && !m.objectBucketOrgLocked(r).ResourceOwnershipEnabled {
			return true
		}
	}
	for _, r := range m.jobs {
		if r.AccountID == accountID {
			return true
		}
	}
	if m.queueOwnershipErasureBlockedLocked(accountID) {
		return true
	}

	for _, r := range m.apiConsumers {
		if r.AccountID == accountID {
			return true
		}
	}
	for _, r := range m.appWebhooks {
		if r.AccountID == accountID {
			return true
		}
	}
	return false
}

// Billing writers use the existing plan table for validation.
func validResourcePlan(plan api.Plan) bool { _, ok := api.LimitsFor(plan); return ok }

func (m *MemStore) updateOrgResourcePlanLocked(org Org, plan api.Plan) error {
	if !validResourcePlan(plan) {
		return ErrInvalidArgument
	}
	if plan == api.PlanFree {
		for id, app := range m.apps {
			if app.OrgID == org.ID && app.StreamingEnabled {
				app.StreamingEnabled = false
				m.apps[id] = app
			}
		}
	}
	org.Plan = plan
	if err := m.validateObjectBillingAdoptionLocked(org); err != nil {
		return err
	}
	org = m.activateOrgResourceOwnershipLocked(org)
	m.orgs[org.ID] = org
	return nil
}

func (m *MemStore) billingResourceAccountLocked(id string) (Account, bool) {
	if account, ok := m.accounts[id]; ok {
		return account, true
	}
	if org, ok := m.orgs[id]; ok && !org.Personal {
		return m.orgResourceAccountLocked(org), true
	}
	return Account{}, false
}

func (m *MemStore) activateOrgResourceOwnershipLocked(org Org) Org {
	if org.ResourceOwnershipEnabled || org.Personal {
		return org
	}
	eligible := org.Plan == api.PlanFree
	for _, identity := range m.billingIdentities {
		eligible = eligible || identity.AccountID == org.ID && identity.CustomerID != "" && identity.SubscriptionID != "" && identity.CustomerID == org.ProviderCustomerID && identity.SubscriptionID == org.StripeSubscriptionItem
	}
	if !eligible {
		return org
	}
	// Billing writers validate this before changing any mutable identity fields.
	// Async reservations stay on their namespace counters (see
	// poolOrgAsyncReservationsLocked).
	org.ResourceOwnershipEnabled = true
	if m.objectOrgOwnedFrom == nil {
		m.objectOrgOwnedFrom = make(map[string]time.Time)
	}
	m.objectOrgOwnedFrom[org.ID] = m.clock().UTC()
	m.invalidateObjectUsageForAdoptionLocked(org)
	return org
}

func (m *MemStore) validateObjectBillingAdoptionLocked(org Org) error {
	if org.Personal || org.ResourceOwnershipEnabled || org.Plan != api.PlanFree && (org.ProviderCustomerID == "" || org.StripeSubscriptionItem == "") {
		return nil
	}
	for _, b := range m.objectBuckets {
		if m.objectBucketOrgLocked(b).ID != org.ID {
			continue
		}
		for _, record := range m.objectStorageBilling {
			if record.AccountID == b.AccountID && !record.PeriodStart.Before(ObjectStoragePeriod(m.clock())) {
				return ErrObjectBillingConflict
			}
		}
	}
	return nil
}
