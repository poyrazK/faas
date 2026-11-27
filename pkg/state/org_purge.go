package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// DeletedOrgPurgeStore releases deleted shared organizations' slugs once their
// deletion window has passed (ADR-061 amendment, prod hunt r73). Optional, so
// the legacy Store seam does not widen.
type DeletedOrgPurgeStore interface {
	PurgeDeletedOrgs(ctx context.Context, requestedBefore time.Time, limit int) ([]PurgedOrg, error)
}

// PurgedOrg names an organization whose slug PurgeDeletedOrgs released.
type PurgedOrg struct {
	ID   string
	Slug string // the slug it held before the purge
}

// purgedOrgSlugPrefix starts every released organization's tombstone slug.
const purgedOrgSlugPrefix = "purged-"

// PurgedOrgSlug is the tombstone slug a purged organization keeps: unique (it
// is derived from the id), within orgs_slug_shape, and never a customer's.
func PurgedOrgSlug(id string) string {
	return purgedOrgSlugPrefix + strings.ReplaceAll(id, "-", "")[:24]
}

// IsPurgedOrgSlug reports whether slug is a purge tombstone.
func IsPurgedOrgSlug(slug string) bool {
	return strings.HasPrefix(slug, purgedOrgSlugPrefix)
}

var _ DeletedOrgPurgeStore = (*PgStore)(nil)
var _ DeletedOrgPurgeStore = (*MemStore)(nil)

// PurgeDeletedOrgs releases up to limit shared organizations deleted before
// requestedBefore: each takes its tombstone slug, so the old one can be
// registered again, and its open invitations are revoked. The row stays:
// retained billing, usage and audit records reference it (ON DELETE
// RESTRICT), and members' rows keep the attribution ADR-061 requires.
func (s *PgStore) PurgeDeletedOrgs(ctx context.Context, requestedBefore time.Time, limit int) ([]PurgedOrg, error) {
	if limit <= 0 {
		return nil, errors.New("state: purge deleted orgs: limit must be positive")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("state: purge deleted orgs: %w", err)
	}
	defer rollbackOrgAccess(ctx, tx)
	rows, err := tx.Query(ctx, `
		with picked as (
		    select id, slug
		      from orgs
		     where deleted_pending
		       and not personal_org
		       and coalesce(deletion_requested_at, updated_at) < $1
		       and slug not like 'purged-%'
		     order by coalesce(deletion_requested_at, updated_at)
		     limit $2
		     for update skip locked)
		update orgs o
		   set slug = 'purged-' || substr(replace(o.id::text, '-', ''), 1, 24), updated_at = now()
		  from picked p
		 where o.id = p.id
		returning o.id::text, p.slug`, requestedBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("state: purge deleted orgs: %w", mapErr(err))
	}
	var out []PurgedOrg
	var ids []string
	for rows.Next() {
		var p PurgedOrg
		if err := rows.Scan(&p.ID, &p.Slug); err != nil {
			rows.Close()
			return nil, fmt.Errorf("state: scan purged org: %w", err)
		}
		out = append(out, p)
		ids = append(ids, p.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: purge deleted orgs: %w", mapErr(err))
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if _, err := tx.Exec(ctx, `
		update org_invitations
		   set revoked_at = now()
		 where org_id = any($1::uuid[])
		   and consumed_at is null
		   and revoked_at is null`, ids); err != nil {
		return nil, fmt.Errorf("state: revoke purged org invitations: %w", mapErr(err))
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("state: commit purge deleted orgs: %w", err)
	}
	return out, nil
}

// PurgeDeletedOrgs mirrors PgStore.PurgeDeletedOrgs.
func (m *MemStore) PurgeDeletedOrgs(_ context.Context, requestedBefore time.Time, limit int) ([]PurgedOrg, error) {
	if limit <= 0 {
		return nil, errors.New("memstore: purge deleted orgs: limit must be positive")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []Org
	for _, o := range m.orgs {
		requested := o.UpdatedAt
		if o.DeletionRequestedAt != nil {
			requested = *o.DeletionRequestedAt
		}
		if o.DeletedPending && !o.Personal && requested.Before(requestedBefore) && !IsPurgedOrgSlug(o.Slug) {
			due = append(due, o)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].ID < due[j].ID })
	if len(due) > limit {
		due = due[:limit]
	}
	now := time.Now().UTC()
	out := make([]PurgedOrg, 0, len(due))
	for _, o := range due {
		out = append(out, PurgedOrg{ID: o.ID, Slug: o.Slug})
		delete(m.orgsBySlug, strings.ToLower(o.Slug))
		o.Slug = PurgedOrgSlug(o.ID)
		o.UpdatedAt = now
		m.orgs[o.ID] = o
		m.orgsBySlug[strings.ToLower(o.Slug)] = o.ID
		for key, inv := range m.invitations {
			if inv.OrgID == o.ID && inv.ConsumedAt == nil && inv.RevokedAt == nil {
				revoked := now
				inv.RevokedAt = &revoked
				m.invitations[key] = inv
			}
		}
	}
	return out, nil
}
