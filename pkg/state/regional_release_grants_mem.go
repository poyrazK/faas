package state

import (
	"bytes"
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) regionalGrantApp(g RegionalReleaseGrant) bool {
	app, ok := m.apps[g.AppID]
	return ok && app.AccountID == g.AccountID && app.OrgID == g.OrgID && m.regionalGrantActor(app, g.issuer())
}

// Caller holds m.mu. Shared consent requires a live credential-issuing role;
// the creator's role or plan never substitutes for the consenting member.
func (m *MemStore) regionalGrantActor(app App, actor string) bool {
	if app.Status != AppActive && app.Status != AppEvictedCold {
		return false
	}
	if m.regionalReleasePersonalApp(app) {
		return actor == app.AccountID
	}
	org, ok := m.orgs[app.OrgID]
	if !ok || org.Personal || org.Status != OrgStatusActive || org.DeletedPending {
		return false
	}
	acct, ok := m.accounts[actor]
	if !ok || !acct.MayDeploy() || !acct.EmailVerified() {
		return false
	}
	mem, ok := m.memberships[orgAccountKey{OrgID: app.OrgID, AccountID: actor}]
	return ok && mem.RemovedAt == nil && (mem.Role == OrgRoleOwner || mem.Role == OrgRoleAdmin)
}
func (m *MemStore) PutRegionalReleaseGrant(_ context.Context, g RegionalReleaseGrant) (RegionalReleaseGrant, error) {
	g.ExpiresAt = g.ExpiresAt.UTC().Truncate(time.Microsecond)
	g.IssuerAccountID = g.issuer()
	if !g.valid() {
		return RegionalReleaseGrant{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.regionalGrantApp(g) {
		return RegionalReleaseGrant{}, ErrNotFound
	}
	if old, ok := m.regionalReleaseGrants[g.ID]; ok {
		if !old.matches(g) {
			return RegionalReleaseGrant{}, ErrConflict
		}
		return cloneRegionalReleaseGrant(old), nil
	}
	now := time.Now().UTC()
	if err := validRegionalGrantExpiry(g.ExpiresAt, now); err != nil {
		return RegionalReleaseGrant{}, err
	}
	if head, ok := m.regionalImageReleaseHeads[g.AppID]; ok && (head.CellID != g.CellID || head.AccountID != g.AccountID || head.GlobalAppID != g.GlobalAppID) {
		return RegionalReleaseGrant{}, ErrConflict
	}
	count := int64(1)
	for _, old := range m.regionalReleaseGrants {
		if bytes.Equal(old.Hash, g.Hash) {
			return RegionalReleaseGrant{}, ErrConflict
		}
		if old.AppID == g.AppID && old.Status == "active" && old.ExpiresAt.After(now) {
			count++
		}
	}
	if count > api.RegionalReleaseGrantsPerAppMax {
		return RegionalReleaseGrant{}, &RegionalReleaseGrantLimitError{Limit: api.RegionalReleaseGrantsPerAppMax, Observed: count}
	}
	g.Status, g.CreatedAt, g.RevokedAt = "active", now, nil
	m.regionalReleaseGrants[g.ID] = cloneRegionalReleaseGrant(g)
	return cloneRegionalReleaseGrant(g), nil
}
func (m *MemStore) regionalGrantLocked(owner, cellID, appID, id string) (RegionalReleaseGrant, error) {
	g, ok := m.regionalReleaseGrants[id]
	app, appOK := m.apps[appID]
	if !ok || !appOK || g.CellID != cellID || g.AppID != appID || app.AccountID != g.AccountID || app.OrgID != g.OrgID || !m.regionalGrantActor(app, owner) {
		return RegionalReleaseGrant{}, ErrNotFound
	}
	return g, nil
}
func (m *MemStore) GetRegionalReleaseGrant(_ context.Context, owner, cellID, appID, id string) (RegionalReleaseGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, err := m.regionalGrantLocked(owner, cellID, appID, id)
	return cloneRegionalReleaseGrant(g), err
}
func (m *MemStore) RevokeRegionalReleaseGrant(_ context.Context, owner, cellID, appID, id string) (RegionalReleaseGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, err := m.regionalGrantLocked(owner, cellID, appID, id)
	if err != nil {
		return RegionalReleaseGrant{}, err
	}
	if g.Status != "revoked" {
		now := time.Now().UTC()
		g.Status, g.RevokedAt = "revoked", &now
		m.regionalReleaseGrants[id] = g
	}
	return cloneRegionalReleaseGrant(g), nil
}
func (m *MemStore) AuthenticateRegionalReleaseGrant(_ context.Context, hash []byte) (Account, APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, g := range m.regionalReleaseGrants {
		if !bytes.Equal(g.Hash, hash) {
			continue
		}
		if !m.regionalGrantApp(g) {
			return Account{}, APIKey{}, ErrNotFound
		}
		if err := g.active(time.Now()); err != nil {
			return Account{}, APIKey{}, err
		}
		acct, ok := m.accounts[g.issuer()]
		if !ok {
			return Account{}, APIKey{}, ErrNotFound
		}
		return acct, g.principal(), nil
	}
	return Account{}, APIKey{}, ErrNotFound
}
func (m *MemStore) checkRegionalGrantIntent(i RegionalImageReleaseIntent) error {
	if i.GrantID == "" {
		return nil
	}
	g, err := m.regionalGrantLocked(i.AccountID, i.CellID, i.AppID, i.GrantID)
	if err != nil {
		return err
	}
	if g.GlobalAppID != i.GlobalAppID || i.Generation <= 0 {
		return ErrNotFound
	}
	return g.active(time.Now())
}
