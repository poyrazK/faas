package state

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
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
// An id shorter than 24 characters once its dashes are gone (a MemStore or
// fixture id, never a uuid) is used whole.
func PurgedOrgSlug(id string) string {
	compact := strings.ToLower(strings.ReplaceAll(id, "-", ""))
	if len(compact) > 24 {
		compact = compact[:24]
	}
	return purgedOrgSlugPrefix + compact
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
	q := sqlc.New()
	rows, err := q.PurgeDeletedOrgSlugs(ctx, tx, sqlc.PurgeDeletedOrgSlugsParams{
		RequestedBefore: pgtype.Timestamptz{Time: requestedBefore, Valid: true},
		RowLimit:        int32(min(limit, math.MaxInt32)), // #nosec G115 -- bounded by min
	})
	if err != nil {
		return nil, fmt.Errorf("state: purge deleted orgs: %w", mapErr(err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]PurgedOrg, 0, len(rows))
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		out = append(out, PurgedOrg{ID: row.ID, Slug: row.OldSlug})
		ids = append(ids, mustPgUUID(row.ID))
	}
	if err := q.RevokeOpenOrgInvitations(ctx, tx, ids); err != nil {
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
