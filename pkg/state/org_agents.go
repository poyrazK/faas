package state

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OrgAgentPolicy is whether agents may act in a shared organization and
// which grantable capabilities they may carry (ADR-20261101225122).
type OrgAgentPolicy struct {
	OrgID         string
	AgentsAllowed bool
	AllowedScopes []string
	// UpdatedAt is zero for an organization that never set a policy.
	UpdatedAt time.Time
	UpdatedBy string
}

// DefaultOrgAgentPolicy is the policy of an organization that set none:
// agents are allowed with every grantable capability.
func DefaultOrgAgentPolicy(orgID string) OrgAgentPolicy {
	return OrgAgentPolicy{OrgID: orgID, AgentsAllowed: true, AllowedScopes: slices.Clone(api.MCPGrantableScopes)}
}

// OrgAgentStore keeps organizations' agent policies and the organization a
// hosted MCP sign-in's code acts in. apid is the only writer.
type OrgAgentStore interface {
	// OrgAgentPolicy returns the organization's policy, or the default.
	OrgAgentPolicy(ctx context.Context, orgID string) (OrgAgentPolicy, error)
	// SetOrgAgentPolicy writes the organization's policy.
	SetOrgAgentPolicy(ctx context.Context, policy OrgAgentPolicy) (OrgAgentPolicy, error)
	// SetCliAuthCodeOrg binds a code waiting for approval to an organization
	// ("" for the personal one). ErrNotFound when no such code waits.
	SetCliAuthCodeOrg(ctx context.Context, tokenHash []byte, orgID string) error
	// CliAuthCodeOrg returns the organization a code was bound to, or "".
	CliAuthCodeOrg(ctx context.Context, tokenHash []byte) (string, error)
}

func (s *PgStore) OrgAgentPolicy(ctx context.Context, orgID string) (OrgAgentPolicy, error) {
	row, err := sqlc.New().GetOrgAgentPolicy(ctx, s.pool, mustPgUUID(orgID))
	if err != nil {
		if err = mapErr(err); errors.Is(err, ErrNotFound) {
			return DefaultOrgAgentPolicy(orgID), nil
		}
		return OrgAgentPolicy{}, err
	}
	return orgAgentPolicyFromRow(row), nil
}

func (s *PgStore) SetOrgAgentPolicy(ctx context.Context, policy OrgAgentPolicy) (OrgAgentPolicy, error) {
	var updatedBy pgtype.UUID
	if policy.UpdatedBy != "" {
		updatedBy = mustPgUUID(policy.UpdatedBy)
	}
	row, err := sqlc.New().UpsertOrgAgentPolicy(ctx, s.pool, sqlc.UpsertOrgAgentPolicyParams{
		OrgID: mustPgUUID(policy.OrgID), AgentsAllowed: policy.AgentsAllowed, AllowedScopes: nonNilStrings(policy.AllowedScopes), UpdatedBy: updatedBy,
	})
	if err != nil {
		return OrgAgentPolicy{}, mapErr(err)
	}
	return orgAgentPolicyFromRow(row), nil
}

func orgAgentPolicyFromRow(row sqlc.OrgAgentPolicy) OrgAgentPolicy {
	return OrgAgentPolicy{OrgID: pgUUIDString(row.OrgID), AgentsAllowed: row.AgentsAllowed, AllowedScopes: nonNilStrings(row.AllowedScopes), UpdatedAt: row.UpdatedAt.Time, UpdatedBy: pgUUIDString(row.UpdatedBy)}
}

func (s *PgStore) SetCliAuthCodeOrg(ctx context.Context, tokenHash []byte, orgID string) error {
	var org pgtype.UUID
	if orgID != "" {
		org = mustPgUUID(orgID)
	}
	_, err := firstCredential(ctx, credentialLookups(credTagCLIAuthCode, tokenHash), func(ctx context.Context, value []byte) (struct{}, error) {
		n, err := sqlc.New().SetCliAuthCodeOrg(ctx, s.pool, sqlc.SetCliAuthCodeOrgParams{OrgID: org, TokenHash: value})
		if err != nil {
			return struct{}{}, mapErr(err)
		}
		if n == 0 {
			return struct{}{}, ErrNotFound
		}
		return struct{}{}, nil
	})
	return err
}

func (s *PgStore) CliAuthCodeOrg(ctx context.Context, tokenHash []byte) (string, error) {
	return firstCredential(ctx, credentialLookups(credTagCLIAuthCode, tokenHash), func(ctx context.Context, value []byte) (string, error) {
		org, err := sqlc.New().GetCliAuthCodeOrg(ctx, s.pool, value)
		if err != nil {
			return "", mapErr(err)
		}
		return pgUUIDString(org), nil
	})
}

func (m *MemStore) OrgAgentPolicy(_ context.Context, orgID string) (OrgAgentPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if policy, ok := m.orgAgentPolicies[orgID]; ok {
		policy.AllowedScopes = slices.Clone(policy.AllowedScopes)
		return policy, nil
	}
	return DefaultOrgAgentPolicy(orgID), nil
}

func (m *MemStore) SetOrgAgentPolicy(_ context.Context, policy OrgAgentPolicy) (OrgAgentPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.orgs[policy.OrgID]; !ok {
		return OrgAgentPolicy{}, ErrNotFound
	}
	for _, scope := range policy.AllowedScopes {
		if !slices.Contains(api.MCPGrantableScopes, scope) {
			return OrgAgentPolicy{}, ErrConflict
		}
	}
	if m.orgAgentPolicies == nil {
		m.orgAgentPolicies = map[string]OrgAgentPolicy{}
	}
	policy.AllowedScopes = nonNilStrings(slices.Clone(policy.AllowedScopes))
	policy.UpdatedAt = time.Now()
	m.orgAgentPolicies[policy.OrgID] = policy
	return policy, nil
}

func (m *MemStore) SetCliAuthCodeOrg(_ context.Context, tokenHash []byte, orgID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := rawCredentialKey(m.cliAuthCodes, credTagCLIAuthCode, tokenHash)
	row := m.cliAuthCodes[key]
	if !ok || row.AccountID != "" || !row.ExpiresAt.After(time.Now()) {
		return ErrNotFound
	}
	row.OrgID = orgID
	m.cliAuthCodes[key] = row
	return nil
}

func (m *MemStore) CliAuthCodeOrg(_ context.Context, tokenHash []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := rawCredentialKey(m.cliAuthCodes, credTagCLIAuthCode, tokenHash)
	if !ok {
		return "", ErrNotFound
	}
	return m.cliAuthCodes[key].OrgID, nil
}
