package globaldirectory

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func NewMemoryStoreWithOrgAuthority(authority OrgAuthority) *MemoryStore {
	s := NewMemoryStore()
	s.orgAuthority = authority
	return s
}
func (s *MemoryStore) OwnerByID(_ context.Context, id string) (Owner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.owners[id]
	if !ok {
		return Owner{}, ErrNotFound
	}
	return o, nil
}
func (s *MemoryStore) ResolveOrgOwner(ctx context.Context, orgID string) (Owner, error) {
	if _, err := activeSharedOrg(ctx, s.orgAuthority, orgID); err != nil {
		return Owner{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.owners {
		if o.OrgID == orgID {
			return o, nil
		}
	}
	o := Owner{ID: uuid.NewString(), OrgID: orgID}
	s.owners[o.ID] = o
	return o, nil
}
func (s *MemoryStore) CreateOrgApp(ctx context.Context, ownerID, actorID string, plan api.Plan, slug string, limit int) (App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.owners[ownerID]
	if !ok || !o.Shared() {
		return App{}, ErrNotFound
	}
	org, err := activeSharedOrg(ctx, s.orgAuthority, o.OrgID)
	if err != nil {
		return App{}, err
	}
	acct, err := s.orgAuthority.AccountByID(ctx, actorID)
	if errors.Is(err, state.ErrNotFound) {
		return App{}, ErrOwnerForbidden
	}
	if err != nil {
		return App{}, err
	}
	if !acct.MayDeploy() || !acct.EmailVerified() {
		return App{}, ErrOwnerForbidden
	}
	mem, err := s.orgAuthority.OrgMemberByAccount(ctx, o.OrgID, actorID)
	if errors.Is(err, state.ErrNotFound) {
		return App{}, ErrOwnerForbidden
	}
	if err != nil {
		return App{}, err
	}
	if mem.RemovedAt != nil || (mem.Role != state.OrgRoleOwner && mem.Role != state.OrgRoleAdmin && mem.Role != state.OrgRoleDeveloper) {
		return App{}, ErrOwnerForbidden
	}
	if org.Plan != plan {
		return App{}, ErrOwnerPlanChanged
	}
	return s.createApp(ownerID, slug, limit)
}

var _ OwnerStore = (*MemoryStore)(nil)
