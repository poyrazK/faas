package state

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolinvocation"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// DelegatedToolInvocationStore checks grant, key and app authority while holding
// the same locks that linearize dispatch. A preflight check alone is insufficient.
type DelegatedToolInvocationStore interface {
	BeginDelegatedToolInvocation(context.Context, string, string, api.ToolInvocationIntent, api.ToolInvocationTarget, api.ToolInvocationDelegation) (api.ToolInvocationReceipt, error)
	ChangeDelegatedToolInvocation(context.Context, string, string, api.ToolInvocationDelegation, string, *api.ToolInvocationOutcome) (api.ToolInvocationReceipt, error)
}

func validDelegatedBinding(g api.ToolGrantRecord, d api.ToolInvocationDelegation, in api.ToolInvocationIntent, target api.ToolInvocationTarget) bool {
	return toolgrant.Active(g, time.Now()) && g.Intent.AllowInvocation && g.ID == d.GrantID && g.Intent.KeyID == d.KeyID && g.ProjectID == d.ProjectID &&
		g.Intent.Version == in.Version && g.Intent.Name == in.Name && g.AppID == target.AppID && g.App == target.App && toolgrant.CredentialMatches(g, in.AccessMode, in.CredentialBindingSHA256)
}

func (m *MemStore) delegatedGrantLocked(account string, d api.ToolInvocationDelegation) (api.ToolGrantRecord, error) {
	g, ok := m.toolGrants[account+":"+d.GrantID]
	if !ok || !toolgrant.Active(g, time.Now()) || !g.Intent.AllowInvocation || g.Intent.KeyID != d.KeyID || g.ProjectID != d.ProjectID {
		return g, ErrNotFound
	}
	k, ok := m.keys[d.KeyID]
	if !ok || k.AccountID != account || (k.Status != "active" && k.Status != "grace") || k.RevokedAt != nil || (k.ExpiresAt != nil && !time.Now().Before(*k.ExpiresAt)) ||
		!slices.Contains(k.Scopes, api.ScopeAppsRead) || slices.Contains(k.Scopes, api.ScopeAdmin) || k.Label != api.MCPLoginKeyLabel && !slices.Contains(k.Scopes, api.ScopeAgent) || !m.delegatedPersonalOrgLocked(account, k.OrgID) {
		return g, ErrNotFound
	}
	a, ok := m.apps[g.AppID]
	if !ok || a.AccountID != account || a.Slug != g.App || a.ProjectID != g.ProjectID || !m.delegatedPersonalOrgLocked(account, a.OrgID) {
		return g, ErrNotFound
	}
	if projects, limited := m.keyProjectLimits[k.ID]; limited && !slices.Contains(projects, a.ProjectID) {
		return g, ErrNotFound
	}
	if g.Credential != nil {
		if g.Credential.ConnectionID != "" {
			if err := m.toolOAuthGrantLocked(g, false); err != nil {
				return g, err
			}
			return g, nil
		}
		secret, ok := m.secrets[secretKey{AppID: g.AppID, Scope: g.Credential.Scope, Key: strings.TrimPrefix(g.Intent.CredentialSecretRef, api.SecretRefPrefix)}]
		if !ok || secret.AccountID != account || secret.ManagedPostgresBindingID != "" || secret.ManagedObjectStorageCredentialID != "" || secret.ManagedCredentialRef != "" || toolpublication.Digest(secret.Ciphertext) != g.Credential.SourceSHA256 {
			return g, ErrNotFound
		}
	}
	return g, nil
}

func (m *MemStore) delegatedPersonalOrgLocked(account, org string) bool {
	if org == "" {
		return true
	}
	o, ok := m.orgs[org]
	return ok && o.Personal && o.PersonalOwnerAccountID != nil && *o.PersonalOwnerAccountID == account
}

func (m *MemStore) BeginDelegatedToolInvocation(ctx context.Context, account, id string, in api.ToolInvocationIntent, target api.ToolInvocationTarget, d api.ToolInvocationDelegation) (api.ToolInvocationReceipt, error) {
	return m.beginToolInvocation(ctx, account, id, in, target, &d)
}

func (m *MemStore) ChangeDelegatedToolInvocation(_ context.Context, account, id string, d api.ToolInvocationDelegation, action string, out *api.ToolInvocationOutcome) (api.ToolInvocationReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, err := m.delegatedGrantLocked(account, d)
	if err != nil {
		return api.ToolInvocationReceipt{}, err
	}
	if action == "dispatch" && g.Credential != nil && g.Credential.ConnectionID != "" {
		if err := m.toolOAuthGrantLocked(g, true); err != nil {
			return api.ToolInvocationReceipt{}, err
		}
	}
	key := account + ":" + id
	record, ok := m.toolInvocations[key]
	if !ok || !reflect.DeepEqual(record.Delegation, &d) || !validDelegatedBinding(g, d, record.Intent, record.Target) {
		return api.ToolInvocationReceipt{}, ErrNotFound
	}
	result, err := toolinvocation.Change(record, action, out, time.Now().UTC())
	if err != nil {
		return result, invocationError(err)
	}
	m.toolInvocations[key] = cloneToolInvocation(result.Record)
	return result, nil
}
