package globaldirectory

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type memoryRecovery struct {
	RecoveryWork
	due, until time.Time
}

func (s *MemoryStore) wake(app App) {
	old := s.recovery[app.ID]
	old.GlobalAppID, old.AccountID, old.Slug, old.WakeID = app.ID, app.AccountID, app.Slug, uuid.NewString()
	old.due = time.Now().UTC()
	s.recovery[app.ID] = old
}
func (s *MemoryStore) retireDelegations(appID, region string) {
	for id, g := range s.delegations {
		if g.GlobalAppID != appID || region != "" && g.Binding.Region != region || !g.usable(time.Now()) {
			continue
		}
		g.Status = DelegationRevoking
		s.delegations[id] = g
		s.wake(App{ID: g.GlobalAppID, AccountID: g.AccountID, Slug: g.Slug})
	}
}
func (s *MemoryStore) delegationTarget(appID, region string) (Target, bool) {
	t, ok := s.targets[appID][region]
	c := s.cells[region]
	t.APIBase, t.CellEnabled, t.RegistryGeneration = c.APIBase, c.Enabled, c.Generation
	return t, ok
}
func (s *MemoryStore) EnsureDelegation(_ context.Context, owner, slug string, g Delegation) (Delegation, error) {
	if !g.identityValid() || len(g.Sealed) == 0 || len(g.Sealed) > api.GlobalDelegationCiphertextMaxBytes || !g.ExpiresAt.After(time.Now()) || time.Until(g.ExpiresAt) > api.RegionalReleaseGrantMaxLifetime {
		return Delegation{}, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	app, err := s.app(owner, slug)
	if err != nil {
		return Delegation{}, err
	}
	current, ok := s.delegationTarget(app.ID, g.Binding.Region)
	if !ok || g.AccountID != app.AccountID || g.Slug != app.Slug || !g.matches(current) {
		return Delegation{}, ErrStaleCell
	}
	count := int64(0)
	for id, old := range s.delegations {
		if old.GlobalAppID != app.ID {
			continue
		}
		if !old.ExpiresAt.After(time.Now()) && old.Status != DelegationRevoked && old.Status != DelegationExpired {
			old.Status, old.Sealed = DelegationExpired, nil
			s.delegations[id] = old
		}
		if old.usable(time.Now()) && old.Binding.Region == g.Binding.Region {
			if old.matches(current) {
				return copyDelegation(old), nil
			}
			old.Status = DelegationRevoking
			s.delegations[id] = old
		}
		if old.Status != DelegationRevoked && old.Status != DelegationExpired {
			count++
		}
	}
	if count >= api.GlobalDelegationsPerAppMax {
		return Delegation{}, &DelegationLimitError{Limit: api.GlobalDelegationsPerAppMax, Observed: count + 1}
	}
	if _, ok := s.delegations[g.ID]; ok {
		return Delegation{}, ErrConflict
	}
	g.Status, g.CreatedAt = DelegationPending, time.Now().UTC()
	s.delegations[g.ID] = copyDelegation(g)
	s.wake(app)
	return copyDelegation(g), nil
}
func (s *MemoryStore) CurrentDelegation(_ context.Context, owner, slug, region string) (Delegation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	app, err := s.app(owner, slug)
	if err != nil {
		return Delegation{}, err
	}
	current, ok := s.delegationTarget(app.ID, region)
	for _, g := range s.delegations {
		if g.GlobalAppID == app.ID && g.Binding.Region == region && g.usable(time.Now()) && ok && g.matches(current) {
			return copyDelegation(g), nil
		}
	}
	return Delegation{}, ErrNotFound
}
func (s *MemoryStore) ReadDelegation(_ context.Context, owner, appID, id string) (Delegation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.delegations[id]
	if !ok || g.AccountID != owner || g.GlobalAppID != appID {
		return Delegation{}, ErrNotFound
	}
	return copyDelegation(g), nil
}
func (s *MemoryStore) checkDelegation(g Delegation) error {
	app, err := s.app(g.AccountID, g.Slug)
	if err != nil || app.ID != g.GlobalAppID {
		return ErrStaleCell
	}
	current, ok := s.delegationTarget(g.GlobalAppID, g.Binding.Region)
	if !g.usable(time.Now()) || !ok || !g.matches(current) {
		return ErrStaleCell
	}
	return nil
}
func (s *MemoryStore) CheckDelegation(_ context.Context, owner, appID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.delegations[id]
	if !ok || g.AccountID != owner || g.GlobalAppID != appID {
		return ErrNotFound
	}
	return s.checkDelegation(g)
}
func (s *MemoryStore) SetDelegationStatus(_ context.Context, owner, appID, id, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.delegations[id]
	if !ok || g.AccountID != owner || g.GlobalAppID != appID {
		return ErrNotFound
	}
	if g.Status == DelegationRevoked || g.Status == DelegationExpired {
		if g.Status == status {
			return nil
		}
		return ErrConflict
	}
	switch status {
	case DelegationActive:
		if err := s.checkDelegation(g); err != nil {
			return err
		}
	case DelegationRevoking, DelegationRevoked:
	case DelegationExpired:
		if g.ExpiresAt.After(time.Now()) {
			return ErrConflict
		}
	default:
		return ErrConflict
	}
	g.Status = status
	if status == DelegationRevoked || status == DelegationExpired {
		g.Sealed = nil
	}
	s.delegations[id] = g
	return nil
}
func (s *MemoryStore) ListRecoveryDelegations(_ context.Context, owner, appID string) ([]Delegation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Delegation{}
	for _, g := range s.delegations {
		if g.GlobalAppID == appID && g.AccountID == owner && g.Status != DelegationRevoked && g.Status != DelegationExpired {
			out = append(out, copyDelegation(g))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (s *MemoryStore) ClaimRecovery(_ context.Context, claimID string) ([]RecoveryWork, error) {
	if !ValidID(claimID) {
		return nil, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := []string{}
	for id, w := range s.recovery {
		if !w.due.After(time.Now()) && !w.until.After(time.Now()) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return s.recovery[ids[i]].due.Before(s.recovery[ids[j]].due) })
	if len(ids) > api.GlobalRecoveryBatchApps {
		ids = ids[:api.GlobalRecoveryBatchApps]
	}
	out := make([]RecoveryWork, 0, len(ids))
	for _, id := range ids {
		w := s.recovery[id]
		w.ClaimID = claimID
		w.until = time.Now().Add(api.GlobalRecoveryLease)
		w.ReleaseID = ""
		for _, r := range s.releases[id] {
			if r.Generation == s.releaseHeads[id] {
				w.ReleaseID = r.ReleaseID
			}
		}
		s.recovery[id] = w
		out = append(out, w.RecoveryWork)
	}
	return out, nil
}
func (s *MemoryStore) FinishRecovery(_ context.Context, claim RecoveryWork) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.recovery[claim.GlobalAppID]
	if !ok || w.ClaimID != claim.ClaimID {
		return ErrConflict
	}
	if w.WakeID == claim.WakeID {
		w.due = time.Now().Add(api.GlobalRecoveryInterval)
	}
	w.ClaimID = ""
	w.CursorID = claim.CursorID
	w.until = time.Time{}
	s.recovery[claim.GlobalAppID] = w
	return nil
}
