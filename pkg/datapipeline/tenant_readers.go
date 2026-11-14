package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

func (s *Service) TenantReaders(ctx context.Context, account, id string, revision int64, action, approval string, members []api.DataPipelineTenantMembership) error {
	if s == nil || s.Store == nil || s.Allowed == nil || !s.Allowed(ctx, account) {
		return ErrUnavailable
	}
	if !validID(account) || !validID(id) || revision < 1 || !validMemberships(members) {
		return ErrInvalid
	}
	if action != "preview" && action != "approve" && action != "discard" {
		return ErrInvalid
	}
	if action != "preview" && len(members) != 0 {
		return ErrInvalid
	}
	p, err := s.Store.Get(ctx, account, id)
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	access := tenantAccess(p)
	if p.Revision != revision || access == nil || p.ServingInvalid {
		return ErrConflict
	}
	if s.CanCreate != nil && !s.CanCreate(ctx, account) && action != "discard" {
		before, after := access.Memberships, members
		if action == "approve" {
			if !canApproveReaders(p) || approval != p.ReaderChange.ID || !p.ReaderChange.Tenant {
				return ErrConflict
			}
			before, after = p.ReaderChange.BeforeMemberships, p.ReaderChange.AfterMemberships
		}
		if len(after) >= len(before) || !membershipsSubset(before, after) {
			return ErrUnavailable
		}
	}
	store, ok := s.Store.(interface {
		TenantReaders(context.Context, string, string, int64, string, string, []api.DataPipelineTenantMembership) error
	})
	if !ok {
		return ErrUnavailable
	}
	return store.TenantReaders(ctx, account, id, revision, action, approval, members)
}

func (s *PostgresStore) TenantReaders(ctx context.Context, account, id string, revision int64, action, approval string, members []api.DataPipelineTenantMembership) error {
	p, err := s.Get(ctx, account, id)
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if p.Revision != revision || tenantAccess(p) == nil || p.ServingInvalid {
		return ErrConflict
	}
	c := p.ReaderChange
	switch action {
	case "preview":
		if !CanChangeReaders(p) || !validMemberships(members) {
			return ErrConflict
		}
		after := append([]api.DataPipelineTenantMembership{}, members...)
		slices.SortFunc(after, compareMemberships)
		before := tenantAccess(p).Memberships
		if slices.Equal(before, after) {
			return ErrInvalid
		}
		c = &ReaderChange{Tenant: true, State: "preview", Generation: p.Generation, PreviewRevision: revision + 1, Before: slices.Clone(p.ReadSubjects), After: membershipSubjects(after), Tables: slices.Clone(p.Tables), BeforeMemberships: slices.Clone(before), AfterMemberships: after}
		c.ID = readerChangeID(p, *c)
	case "approve":
		if !canApproveReaders(p) || !c.Tenant || approval != c.ID {
			return ErrConflict
		}
		copy := *c
		copy.State = "approved"
		c = &copy
	case "discard":
		if c == nil || !c.Tenant || c.State != "preview" {
			return ErrConflict
		}
		c = nil
	default:
		return ErrInvalid
	}
	encoded := []byte("{}")
	if c != nil {
		encoded, _ = json.Marshal(c)
	}
	n, err := s.q.PipelineReaders(ctx, s.pool, sqlc.PipelineReadersParams{AccountID: account, ID: id, Revision: revision, ReaderChange: encoded})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
