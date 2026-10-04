package globaldirectory

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func delegationRow(r sqlc.GlobalReleaseDelegation) Delegation {
	return Delegation{ID: uuid.UUID(r.ID.Bytes).String(), GlobalAppID: uuid.UUID(r.GlobalAppID.Bytes).String(), OwnerID: uuid.UUID(r.OwnerID.Bytes).String(), Slug: r.GlobalAppSlug,
		Binding: Binding{CellID: uuid.UUID(r.CellID.Bytes).String(), Region: r.Region, CellGeneration: r.CellGeneration, RegionalAccountID: uuid.UUID(r.RegionalAccountID.Bytes).String(), RegionalOrgID: uuidText(r.RegionalOrgID), RegionalAppID: uuid.UUID(r.RegionalAppID.Bytes).String(), RegionalAppSlug: r.RegionalAppSlug},
		APIBase: r.ApiBase, Hash: append([]byte(nil), r.TokenSha256...), Sealed: append([]byte(nil), r.SealedCredential...), ExpiresAt: r.ExpiresAt.Time, CreatedAt: r.CreatedAt.Time, Status: r.Status}
}
func delegationError(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) && p.ConstraintName == "global_delegation_limit" {
		n, e := strconv.ParseInt(p.Detail, 10, 64)
		if e == nil {
			return &DelegationLimitError{Limit: api.GlobalDelegationsPerAppMax, Observed: n}
		}
	}
	return err
}
func readDelegation(ctx context.Context, db sqlc.DBTX, owner, appID, id string) (Delegation, error) {
	r, err := (&sqlc.Queries{}).ReadGlobalDelegation(ctx, db, sqlc.ReadGlobalDelegationParams{GrantID: idArg(id), OwnerID: idArg(owner), GlobalAppID: idArg(appID)})
	return delegationRow(r), err
}
func (s *PgStore) checkDelegation(ctx context.Context, tx pgx.Tx, g Delegation) error {
	app, err := (&sqlc.Queries{}).LockGlobalAppIdentity(ctx, tx, sqlc.LockGlobalAppIdentityParams{OwnerID: idArg(g.OwnerID), Slug: g.Slug})
	if err != nil {
		return err
	}
	if app.GlobalAppID != g.GlobalAppID || !g.usable(time.Now()) {
		return ErrStaleCell
	}
	r := Release{GlobalAppID: g.GlobalAppID, Targets: []ReleaseTarget{{Target: Target{Binding: g.Binding, GlobalAppID: g.GlobalAppID, APIBase: g.APIBase}}}}
	return s.checkReleaseSnapshot(ctx, tx, r, g.Binding.Region)
}
func (s *PgStore) EnsureDelegation(ctx context.Context, owner, slug string, g Delegation) (Delegation, error) {
	if !g.identityValid() || len(g.Sealed) == 0 || len(g.Sealed) > api.GlobalDelegationCiphertextMaxBytes || !g.ExpiresAt.After(time.Now()) || time.Until(g.ExpiresAt) > api.RegionalReleaseGrantMaxLifetime {
		return Delegation{}, ErrConflict
	}
	var out Delegation
	err := s.transaction(ctx, "ensure delegation", func(tx pgx.Tx) error {
		q := &sqlc.Queries{}
		app, err := q.LockGlobalAppIdentity(ctx, tx, sqlc.LockGlobalAppIdentityParams{OwnerID: idArg(owner), Slug: slug})
		if err != nil {
			return err
		}
		if g.GlobalAppID != app.GlobalAppID || g.OwnerID != app.OwnerID || g.Slug != app.Slug {
			return ErrNotFound
		}
		if err := s.checkDelegation(ctx, tx, g); err != nil {
			return err
		}
		if err := q.ExpireGlobalDelegations(ctx, tx, idArg(app.GlobalAppID)); err != nil {
			return err
		}
		row, err := q.LiveGlobalDelegation(ctx, tx, sqlc.LiveGlobalDelegationParams{GlobalAppID: idArg(app.GlobalAppID), Region: g.Binding.Region})
		if err == nil {
			old := delegationRow(row)
			if old.Binding == g.Binding && old.APIBase == g.APIBase {
				out = old
				return nil
			}
			if _, err = q.SetGlobalDelegationStatus(ctx, tx, sqlc.SetGlobalDelegationStatusParams{GrantID: idArg(old.ID), GlobalAppID: idArg(old.GlobalAppID), OwnerID: idArg(old.OwnerID), Status: DelegationRevoking}); err != nil {
				return err
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		row, err = q.InsertGlobalDelegation(ctx, tx, sqlc.InsertGlobalDelegationParams{GrantID: idArg(g.ID), GlobalAppID: idArg(g.GlobalAppID), OwnerID: idArg(g.OwnerID), Slug: g.Slug, CellID: idArg(g.Binding.CellID), Region: g.Binding.Region, CellGeneration: g.Binding.CellGeneration, ApiBase: g.APIBase, RegionalAccountID: idArg(g.Binding.RegionalAccountID), RegionalOrgID: idArg(g.Binding.RegionalOrgID), RegionalAppID: idArg(g.Binding.RegionalAppID), RegionalAppSlug: g.Binding.RegionalAppSlug, TokenSha256: g.Hash, SealedCredential: g.Sealed, ExpiresAt: pgtype.Timestamptz{Time: g.ExpiresAt, Valid: true}})
		out = delegationRow(row)
		return delegationError(err)
	})
	return out, err
}
func (s *PgStore) CurrentDelegation(ctx context.Context, owner, slug, region string) (Delegation, error) {
	var out Delegation
	err := s.transaction(ctx, "current delegation", func(tx pgx.Tx) error {
		app, err := (&sqlc.Queries{}).LockGlobalAppIdentity(ctx, tx, sqlc.LockGlobalAppIdentityParams{OwnerID: idArg(owner), Slug: slug})
		if err != nil {
			return err
		}
		r, err := (&sqlc.Queries{}).LiveGlobalDelegation(ctx, tx, sqlc.LiveGlobalDelegationParams{GlobalAppID: idArg(app.GlobalAppID), Region: region})
		if err != nil {
			return err
		}
		out = delegationRow(r)
		return s.checkDelegation(ctx, tx, out)
	})
	return out, err
}
func (s *PgStore) ReadDelegation(ctx context.Context, owner, appID, id string) (Delegation, error) {
	g, err := readDelegation(ctx, s.pool, owner, appID, id)
	return g, directoryError("read delegation", err)
}
func (s *PgStore) CheckDelegation(ctx context.Context, owner, appID, id string) error {
	return s.transaction(ctx, "check delegation", func(tx pgx.Tx) error {
		g, err := readDelegation(ctx, tx, owner, appID, id)
		if err != nil {
			return err
		}
		return s.checkDelegation(ctx, tx, g)
	})
}
func (s *PgStore) SetDelegationStatus(ctx context.Context, owner, appID, id, status string) error {
	return s.transaction(ctx, "set delegation status", func(tx pgx.Tx) error {
		q := &sqlc.Queries{}
		g, err := readDelegation(ctx, tx, owner, appID, id)
		if err != nil {
			return err
		}
		// Account/app deletion retains this row; retirement can finish without them.
		_, err = q.LockGlobalAppIdentity(ctx, tx, sqlc.LockGlobalAppIdentityParams{OwnerID: idArg(owner), Slug: g.Slug})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if status == DelegationActive {
			if err := s.checkDelegation(ctx, tx, g); err != nil {
				return err
			}
		}
		n, err := q.SetGlobalDelegationStatus(ctx, tx, sqlc.SetGlobalDelegationStatusParams{GrantID: idArg(id), GlobalAppID: idArg(appID), OwnerID: idArg(owner), Status: status})
		if err == nil && n == 0 {
			return ErrNotFound
		}
		return err
	})
}
func (s *PgStore) ListRecoveryDelegations(ctx context.Context, owner, appID string) ([]Delegation, error) {
	rows, err := (&sqlc.Queries{}).ListRecoveryDelegations(ctx, s.pool, sqlc.ListRecoveryDelegationsParams{GlobalAppID: idArg(appID), OwnerID: idArg(owner)})
	out := make([]Delegation, 0, len(rows))
	for _, row := range rows {
		out = append(out, delegationRow(row))
	}
	return out, directoryError("list recovery delegations", err)
}
func (s *PgStore) ClaimRecovery(ctx context.Context, claimID string) ([]RecoveryWork, error) {
	if !ValidID(claimID) {
		return nil, ErrConflict
	}
	var out []RecoveryWork
	err := s.transaction(ctx, "claim recovery", func(tx pgx.Tx) error {
		rows, err := (&sqlc.Queries{}).ClaimGlobalRecovery(ctx, tx, sqlc.ClaimGlobalRecoveryParams{BatchLimit: api.GlobalRecoveryBatchApps, ClaimID: idArg(claimID), LeaseSeconds: int32(api.GlobalRecoveryLease / time.Second)})
		for _, r := range rows {
			out = append(out, RecoveryWork{GlobalAppID: r.GlobalAppID, OwnerID: r.OwnerID, Slug: r.GlobalAppSlug, ReleaseID: r.ReleaseID, ClaimID: r.ClaimID, WakeID: r.WakeID, CursorID: r.CursorID})
		}
		return err
	})
	return out, err
}
func (s *PgStore) FinishRecovery(ctx context.Context, w RecoveryWork) error {
	n, err := (&sqlc.Queries{}).FinishGlobalRecovery(ctx, s.pool, sqlc.FinishGlobalRecoveryParams{GlobalAppID: idArg(w.GlobalAppID), ClaimID: idArg(w.ClaimID), WakeID: idArg(w.WakeID), CursorID: w.CursorID, RetrySeconds: int32(api.GlobalRecoveryInterval / time.Second)})
	if err == nil && n == 0 {
		return ErrConflict
	}
	return directoryError("finish recovery", err)
}

var _ DelegationStore = (*PgStore)(nil)
var _ DelegationStore = (*MemoryStore)(nil)
