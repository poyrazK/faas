package state

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func regionalGrantRow(row sqlc.RegionalReleaseGrant) RegionalReleaseGrant {
	g := RegionalReleaseGrant{ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), CellID: pgUUIDString(row.CellID), AccountID: pgUUIDString(row.AccountID), IssuerAccountID: pgUUIDString(row.IssuerAccountID), OrgID: pgUUIDString(row.OrgID), GlobalAppID: pgUUIDString(row.GlobalAppID), Hash: append([]byte(nil), row.TokenSha256...), CreatedAt: row.CreatedAt.Time, ExpiresAt: row.ExpiresAt.Time, Status: row.Status}
	if row.RevokedAt.Valid {
		stamp := row.RevokedAt.Time
		g.RevokedAt = &stamp
	}
	return g
}
func regionalGrantError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		if p.ConstraintName == "regional_grant_limit" {
			observed, parseErr := strconv.ParseInt(p.Detail, 10, 64)
			if parseErr == nil && observed > api.RegionalReleaseGrantsPerAppMax {
				err = &RegionalReleaseGrantLimitError{Limit: api.RegionalReleaseGrantsPerAppMax, Observed: observed}
			} else {
				err = ErrConflict
			}
		} else if p.Code == "P0002" {
			err = ErrNotFound
		} else if p.Code == "23505" || p.Code == "23514" {
			err = ErrConflict
		}
	}
	return fmt.Errorf("state: regional release grant: %w", err)
}
func readRegionalGrant(ctx context.Context, db sqlc.DBTX, owner, cellID, appID, id string) (RegionalReleaseGrant, error) {
	row, err := (&sqlc.Queries{}).GetRegionalReleaseGrant(ctx, db, sqlc.GetRegionalReleaseGrantParams{ActorID: uuidToPgtype(owner), CellID: uuidToPgtype(cellID), AppID: uuidToPgtype(appID), GrantID: uuidToPgtype(id)})
	return regionalGrantRow(row), regionalGrantError(err)
}
func (s *PgStore) PutRegionalReleaseGrant(ctx context.Context, g RegionalReleaseGrant) (RegionalReleaseGrant, error) {
	g.ExpiresAt = g.ExpiresAt.UTC().Truncate(time.Microsecond)
	g.IssuerAccountID = g.issuer()
	if !g.valid() {
		return RegionalReleaseGrant{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RegionalReleaseGrant{}, regionalGrantError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := &sqlc.Queries{}
	org, err := q.LockRegionalReleaseGrantApp(ctx, tx, sqlc.LockRegionalReleaseGrantAppParams{ActorID: uuidToPgtype(g.issuer()), AccountID: uuidToPgtype(g.AccountID), CellID: uuidToPgtype(g.CellID), AppID: uuidToPgtype(g.AppID)})
	if err != nil {
		return RegionalReleaseGrant{}, regionalGrantError(err)
	}
	if pgUUIDString(org) != g.OrgID {
		return RegionalReleaseGrant{}, ErrNotFound
	}
	old, err := readRegionalGrant(ctx, tx, g.issuer(), g.CellID, g.AppID, g.ID)
	if err == nil {
		if !old.matches(g) {
			return RegionalReleaseGrant{}, ErrConflict
		}
		return old, regionalGrantError(tx.Commit(ctx))
	}
	if !errors.Is(err, ErrNotFound) {
		return RegionalReleaseGrant{}, err
	}
	if err := validRegionalGrantExpiry(g.ExpiresAt, time.Now()); err != nil {
		return RegionalReleaseGrant{}, err
	}
	row, err := q.InsertRegionalReleaseGrant(ctx, tx, sqlc.InsertRegionalReleaseGrantParams{GrantID: uuidToPgtype(g.ID), AppID: uuidToPgtype(g.AppID), CellID: uuidToPgtype(g.CellID), AccountID: uuidToPgtype(g.AccountID), IssuerAccountID: uuidToPgtype(g.issuer()), OrgID: uuidToPgtype(g.OrgID), GlobalAppID: uuidToPgtype(g.GlobalAppID), TokenSha256: g.Hash, ExpiresAt: pgtypeTimestamptzFromTime(g.ExpiresAt)})
	if err != nil {
		return RegionalReleaseGrant{}, regionalGrantError(err)
	}
	return regionalGrantRow(row), regionalGrantError(tx.Commit(ctx))
}
func (s *PgStore) GetRegionalReleaseGrant(ctx context.Context, owner, cellID, appID, id string) (RegionalReleaseGrant, error) {
	return readRegionalGrant(ctx, s.pool, owner, cellID, appID, id)
}
func (s *PgStore) RevokeRegionalReleaseGrant(ctx context.Context, owner, cellID, appID, id string) (RegionalReleaseGrant, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RegionalReleaseGrant{}, regionalGrantError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := &sqlc.Queries{}
	if _, err := q.LockRegionalReleaseGrantApp(ctx, tx, sqlc.LockRegionalReleaseGrantAppParams{ActorID: uuidToPgtype(owner), CellID: uuidToPgtype(cellID), AppID: uuidToPgtype(appID)}); err != nil {
		return RegionalReleaseGrant{}, regionalGrantError(err)
	}
	if _, err := readRegionalGrant(ctx, tx, owner, cellID, appID, id); err != nil {
		return RegionalReleaseGrant{}, err
	}
	row, err := q.RevokeRegionalReleaseGrant(ctx, tx, sqlc.RevokeRegionalReleaseGrantParams{ActorID: uuidToPgtype(owner), CellID: uuidToPgtype(cellID), AppID: uuidToPgtype(appID), GrantID: uuidToPgtype(id)})
	if err != nil {
		return RegionalReleaseGrant{}, regionalGrantError(err)
	}
	return regionalGrantRow(row), regionalGrantError(tx.Commit(ctx))
}
func (s *PgStore) AuthenticateRegionalReleaseGrant(ctx context.Context, hash []byte) (Account, APIKey, error) {
	row, err := (&sqlc.Queries{}).GetRegionalReleaseGrantByHash(ctx, s.pool, hash)
	if err != nil {
		return Account{}, APIKey{}, regionalGrantError(err)
	}
	g := regionalGrantRow(row)
	if err := g.active(time.Now()); err != nil {
		return Account{}, APIKey{}, err
	}
	acct, err := s.AccountByID(ctx, g.issuer())
	return acct, g.principal(), err
}
func checkRegionalGrantIntent(ctx context.Context, db sqlc.DBTX, i RegionalImageReleaseIntent) error {
	if i.GrantID == "" {
		return nil
	}
	g, err := readRegionalGrant(ctx, db, i.AccountID, i.CellID, i.AppID, i.GrantID)
	if err != nil {
		return err
	}
	if g.GlobalAppID != i.GlobalAppID || g.OrgID != i.OrgID || i.Generation <= 0 {
		return ErrNotFound
	}
	return g.active(time.Now())
}
