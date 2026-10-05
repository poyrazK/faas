package globaldirectory

// adr: 611
import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state"
)

func uuidText(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

func (s *MemoryStore) orgTargetAuthority(ctx context.Context, owner, actor string) error {
	o := s.owners[owner]
	if !o.Shared() {
		return ErrNotFound
	}
	if _, err := activeSharedOrg(ctx, s.orgAuthority, o.OrgID); err != nil {
		return err
	}
	a, err := s.orgAuthority.AccountByID(ctx, actor)
	if errors.Is(err, state.ErrNotFound) {
		return ErrOwnerForbidden
	}
	if err != nil {
		return err
	}
	if !a.MayDeploy() || !a.EmailVerified() {
		return ErrOwnerForbidden
	}
	m, err := s.orgAuthority.OrgMemberByAccount(ctx, o.OrgID, actor)
	if errors.Is(err, state.ErrNotFound) {
		return ErrOwnerForbidden
	}
	if err != nil {
		return err
	}
	if m.RemovedAt != nil || (m.Role != state.OrgRoleOwner && m.Role != state.OrgRoleAdmin) {
		return ErrOwnerForbidden
	}
	return nil
}

func (s *MemoryStore) AttachOrgTarget(ctx context.Context, owner, actor, slug string, b Binding) (Target, error) {
	if b.Validate() != nil || b.RegionalOrgID == "" {
		return Target{}, ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.orgTargetAuthority(ctx, owner, actor); err != nil {
		return Target{}, err
	}
	a, err := s.app(owner, slug)
	if err != nil {
		return Target{}, err
	}
	return s.attachTarget(a, owner, b)
}

func (s *MemoryStore) DetachOrgTarget(ctx context.Context, owner, actor, slug, region string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.orgTargetAuthority(ctx, owner, actor); err != nil {
		return err
	}
	a, err := s.app(owner, slug)
	if err != nil {
		return err
	}
	return s.detachTarget(a, region)
}

func (s *PgStore) AttachOrgTarget(ctx context.Context, owner, actor, slug string, b Binding) (Target, error) {
	if b.Validate() != nil || b.RegionalOrgID == "" || !validOwnerID(actor) {
		return Target{}, ErrConflict
	}
	return s.attachTarget(ctx, owner, actor, slug, b)
}

func (s *PgStore) DetachOrgTarget(ctx context.Context, owner, actor, slug, region string) error {
	if !validOwnerID(actor) {
		return ErrOwnerForbidden
	}
	return s.detachTarget(ctx, owner, actor, slug, region)
}

var _ OrgTargetStore = (*MemoryStore)(nil)
var _ OrgTargetStore = (*PgStore)(nil)
