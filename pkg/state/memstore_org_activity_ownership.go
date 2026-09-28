package state

import (
	"context"
	"strings"
)

var _ OrgActivityOwnershipTransferMutationStore = (*MemStore)(nil)

func transferOrgOwnershipLocked(m *MemStore, orgID, fromAccountID, toAccountID string) (OrgMembership, OrgMembership, error) {
	from, to, err := orgOwnershipTransferMembersLocked(m, orgID, fromAccountID, toAccountID)
	if err != nil {
		return OrgMembership{}, OrgMembership{}, err
	}
	applyOrgOwnershipTransferLocked(m, from, to)
	from.Role = OrgRoleAdmin
	to.Role = OrgRoleOwner
	return from, to, nil
}

func (m *MemStore) TransferOrgOwnershipWithActivity(_ context.Context, orgID, fromAccountID, toAccountID string, activity OrgActivity) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	from, to, err := orgOwnershipTransferMembersLocked(m, orgID, fromAccountID, toAccountID)
	if err != nil {
		return 0, err
	}
	label := "member"
	if account, ok := m.accounts[toAccountID]; ok && strings.TrimSpace(account.Email) != "" {
		label = account.Email
	}
	activity, err = bindOrgActivityToOwnershipTransfer(activity, from, to, label)
	if err != nil {
		return 0, err
	}
	applyOrgOwnershipTransferLocked(m, from, to)
	return m.enqueueOrgActivityOutboxLocked(activity), nil
}

func orgOwnershipTransferMembersLocked(m *MemStore, orgID, fromAccountID, toAccountID string) (OrgMembership, OrgMembership, error) {
	if fromAccountID == toAccountID {
		return OrgMembership{}, OrgMembership{}, ErrOrgLastOwner
	}
	from, ok := m.memberships[orgAccountKey{OrgID: orgID, AccountID: fromAccountID}]
	if !ok {
		return OrgMembership{}, OrgMembership{}, ErrNotFound
	}
	if from.Role != OrgRoleOwner || from.RemovedAt != nil {
		return OrgMembership{}, OrgMembership{}, ErrOrgLastOwner
	}
	to, ok := m.memberships[orgAccountKey{OrgID: orgID, AccountID: toAccountID}]
	if !ok || to.RemovedAt != nil {
		return OrgMembership{}, OrgMembership{}, ErrNotFound
	}
	if to.Role == OrgRoleOwner {
		return OrgMembership{}, OrgMembership{}, ErrOrgLastOwner
	}
	return from, to, nil
}

func applyOrgOwnershipTransferLocked(m *MemStore, from, to OrgMembership) {
	from.Role = OrgRoleAdmin
	to.Role = OrgRoleOwner
	m.memberships[orgAccountKey{OrgID: from.OrgID, AccountID: from.AccountID}] = from
	m.memberships[orgAccountKey{OrgID: to.OrgID, AccountID: to.AccountID}] = to
}
