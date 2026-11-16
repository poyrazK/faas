package datapipeline

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

// AuthMembershipSnapshot binds authoritative memberships to their verified
// Auth app issuer. Subject IDs alone do not cross Auth app boundaries.
type AuthMembershipSnapshot struct {
	Members []api.DataPipelineTenantMembership
	Issuer  string
}

func validAuthIssuer(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func appliedReaderIssuer(p Pipeline) string {
	if p.ReaderChange == nil {
		return ""
	}
	if p.ReaderChange.State == "approved" {
		return p.ReaderChange.BeforeIssuer
	}
	if p.ReaderChange.State == "applied" {
		return p.ReaderChange.AfterIssuer
	}
	return ""
}

func authReaderChange(p Pipeline, members []api.DataPipelineTenantMembership, until, issuer string) *ReaderChange {
	a := tenantAccess(p)
	c := &ReaderChange{Tenant: true, State: "approved", Generation: p.Generation, PreviewRevision: p.Revision,
		Before: slices.Clone(p.ReadSubjects), After: membershipSubjects(members), Tables: slices.Clone(p.Tables),
		BeforeMemberships: slices.Clone(a.Memberships), AfterMemberships: append([]api.DataPipelineTenantMembership{}, members...), BeforeValidUntil: a.ValidUntil, AfterValidUntil: until}
	c.BeforeIssuer, c.AfterIssuer = appliedReaderIssuer(p), issuer
	c.ID = readerChangeID(p, *c)
	return c
}

// A complete bounded snapshot is authoritative. Duplicate deliveries collapse
// into the same membership set; no event order or caller claims can grant rows.
func (w *Worker) syncAuthMemberships(ctx context.Context, p *Pipeline) error {
	if p.Serving == nil || p.Serving.AuthOrganizations == nil || p.DesiredState == "deleted" || !p.Initialized || p.ReaderChange != nil && (p.ReaderChange.State == "approved" || p.ReaderChange.State == "preview") {
		return nil
	}
	if w.AuthMemberships == nil {
		return nil
	} // The last proof expires even if the worker is offline.
	snapshot, err := w.AuthMemberships(ctx, *p)
	if err != nil {
		return nil //nolint:nilerr // The previous proof expires; ordinary replication may continue.
	}
	members := snapshot.Members
	if !validMemberships(members) || !validAuthIssuer(snapshot.Issuer) {
		return nil
	}
	members = append([]api.DataPipelineTenantMembership{}, members...)
	slices.SortFunc(members, compareMemberships)
	until, _ := time.Parse(time.RFC3339Nano, tenantAccess(*p).ValidUntil)
	if snapshot.Issuer == appliedReaderIssuer(*p) && slices.Equal(members, tenantAccess(*p).Memberships) && time.Until(until) > api.DataPipelineAuthSnapshotTTL/2 {
		return nil
	}
	// Billing/admission closure can revoke, but cannot extend or add a grant.
	validUntil := time.Now().UTC().Add(api.DataPipelineAuthSnapshotTTL)
	if w.Allowed == nil || !w.Allowed(ctx, p.AccountID) || w.AuthCanGrant != nil && !w.AuthCanGrant(ctx, p.AccountID) {
		if snapshot.Issuer != appliedReaderIssuer(*p) {
			members = nil
		}
		members = slices.DeleteFunc(members, func(m api.DataPipelineTenantMembership) bool {
			return !slices.Contains(tenantAccess(*p).Memberships, m)
		})
		if len(members) >= len(tenantAccess(*p).Memberships) {
			return nil
		}
		if until.IsZero() {
			validUntil = time.Unix(0, 0).UTC()
		} else if until.Before(validUntil) {
			validUntil = until
		}
	}
	c := authReaderChange(*p, members, validUntil.Format(time.RFC3339Nano), snapshot.Issuer)
	store, ok := w.Store.(interface {
		StageAuthReaders(context.Context, Pipeline, *ReaderChange) error
	})
	if !ok {
		return ErrUnavailable
	}
	if err = store.StageAuthReaders(ctx, *p, c); err != nil {
		return err
	}
	p.ReaderChange = c
	return nil
}

func (s *PostgresStore) StageAuthReaders(ctx context.Context, p Pipeline, c *ReaderChange) error {
	change, _ := json.Marshal(c)
	binding, _ := json.Marshal(p.Serving.AuthOrganizations)
	n, err := s.q.PipelineStageAuthReaders(ctx, s.pool, sqlc.PipelineStageAuthReadersParams{ID: p.ID, AccountID: p.AccountID, Revision: p.Revision, LeaseToken: p.LeaseToken, Change: change, Binding: binding})
	if err != nil {
		return storeError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
