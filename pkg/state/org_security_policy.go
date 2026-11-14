package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OrgSecurityPolicy is an organization's security settings (ADR
// 20261113180000). RequireMFA keeps members without two-factor
// authentication out of the organization: the org-access snapshot does not
// admit them, so every org route, app, project and credential of the
// organization refuses them until they enroll.
type OrgSecurityPolicy struct {
	OrgID      string
	RequireMFA bool
	UpdatedBy  *string
	UpdatedAt  time.Time
}

// OrgSecurityPolicyStore is implemented by MemStore and PgStore. An
// organization without a row has the zero policy.
type OrgSecurityPolicyStore interface {
	OrgSecurityPolicy(ctx context.Context, orgID string) (OrgSecurityPolicy, error)
	SetOrgRequireMFA(ctx context.Context, orgID string, require bool, updatedBy string) error
}

var (
	_ OrgSecurityPolicyStore = (*MemStore)(nil)
	_ OrgSecurityPolicyStore = (*PgStore)(nil)
)

// orgMFABlocksLocked reports whether the organization requires two-factor
// authentication and the account has none.
func (m *MemStore) orgMFABlocksLocked(orgID, accountID string) bool {
	policy, ok := m.accountFlow.orgPolicies[orgID]
	if !ok || !policy.RequireMFA {
		return false
	}
	acct, ok := m.accounts[accountID]
	return !ok || !acct.MFAEnrolled()
}

func (m *MemStore) OrgSecurityPolicy(_ context.Context, orgID string) (OrgSecurityPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if policy, ok := m.accountFlow.orgPolicies[orgID]; ok {
		return policy, nil
	}
	return OrgSecurityPolicy{OrgID: orgID}, nil
}

func (m *MemStore) SetOrgRequireMFA(_ context.Context, orgID string, require bool, updatedBy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.orgs[orgID]; !ok {
		return ErrNotFound
	}
	if m.accountFlow.orgPolicies == nil {
		m.accountFlow.orgPolicies = map[string]OrgSecurityPolicy{}
	}
	by := updatedBy
	m.accountFlow.orgPolicies[orgID] = OrgSecurityPolicy{OrgID: orgID, RequireMFA: require, UpdatedBy: &by, UpdatedAt: time.Now().UTC()}
	return nil
}

func (s *PgStore) OrgSecurityPolicy(ctx context.Context, orgID string) (OrgSecurityPolicy, error) {
	org, err := parsePgUUID(orgID)
	if err != nil {
		return OrgSecurityPolicy{}, ErrNotFound
	}
	row, err := sqlc.New().OrgSecurityPolicy(ctx, s.pool, org)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrgSecurityPolicy{OrgID: orgID}, nil
	}
	if err != nil {
		return OrgSecurityPolicy{}, fmt.Errorf("read org security policy: %w", mapErr(err))
	}
	policy := OrgSecurityPolicy{OrgID: orgID, RequireMFA: row.RequireMfa, UpdatedAt: row.UpdatedAt.Time}
	if row.UpdatedByAccountID.Valid {
		by := pgUUIDStringNullable(row.UpdatedByAccountID)
		policy.UpdatedBy = &by
	}
	return policy, nil
}

func (s *PgStore) SetOrgRequireMFA(ctx context.Context, orgID string, require bool, updatedBy string) error {
	org, err := parsePgUUID(orgID)
	if err != nil {
		return ErrNotFound
	}
	params := sqlc.SetOrgRequireMFAParams{OrgID: org, RequireMfa: require}
	if by, err := parsePgUUID(updatedBy); err == nil {
		params.UpdatedBy = by
	}
	if err := sqlc.New().SetOrgRequireMFA(ctx, s.pool, params); err != nil {
		return fmt.Errorf("set org require mfa: %w", mapErr(err))
	}
	return nil
}
