package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RegionalRuntimeConsentStore = (*PgStore)(nil)

func runtimeConsentError(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) && p.ConstraintName == "regional_runtime_consent_limit" {
		return &RegionalRuntimeConsentLimitError{Limit: api.RegionalRuntimeConsentsPerAppMax, Observed: api.RegionalRuntimeConsentsPerAppMax + 1}
	}
	if errors.As(err, &p) && p.ConstraintName == "regional_runtime_consent_unavailable" {
		return ErrRegionalRuntimeConsent
	}
	if errors.As(err, &p) && (p.Code == "P0002" || p.ConstraintName == "regional_capacity_frozen") {
		err = ErrNotFound
	}
	return regionalQuotaError(err)
}

func runtimeConsentRow(row sqlc.RegionalRuntimeConsent) (RegionalRuntimeConsent, error) {
	c := runtimequota.Consent{ID: pgUUIDString(row.ID), IssuerID: pgUUIDString(row.IssuerID), GlobalAppID: pgUUIDString(row.GlobalAppID), OwnerID: pgUUIDString(row.OwnerID), ExpiresAtMicros: row.ExpiresAt.Time.UnixMicro()}
	if json.Unmarshal(row.Binding, &c.Binding) != nil || c.Validate() != nil {
		return RegionalRuntimeConsent{}, ErrConflict
	}
	return RegionalRuntimeConsent{Consent: c, CreatedAt: row.CreatedAt.Time, RevokedAt: timestamptzToTimePtr(row.RevokedAt)}, nil
}

func readRuntimeConsent(ctx context.Context, db sqlc.DBTX, id string) (RegionalRuntimeConsent, error) {
	row, err := (&sqlc.Queries{}).RegionalRuntimeConsent(ctx, db, uuidToPgtype(id))
	if err != nil {
		return RegionalRuntimeConsent{}, runtimeConsentError(err)
	}
	return runtimeConsentRow(row)
}

func lockRuntimeConsentOwner(ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, b runtimequota.Binding) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return q.LockRegionalRuntimeConsentOwner(ctx, db, raw)
}

func checkDelegatedRuntimeConsent(ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, id string, g runtimequota.Grant) error {
	c, err := readRuntimeConsent(ctx, db, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err != nil || !runtimeConsentAllows(c, g, time.Now()) {
		return ErrRegionalRuntimeConsent
	}
	if err := lockRuntimeConsentOwner(ctx, db, q, g.Binding); err != nil {
		err = runtimeConsentError(err)
		if errors.Is(err, ErrNotFound) {
			return ErrRegionalRuntimeConsent
		}
		return err
	}
	// Owner locks can wait past expiry, including on an existing receipt retry.
	if !runtimeConsentAllows(c, g, time.Now()) {
		return ErrRegionalRuntimeConsent
	}
	return nil
}

func (s *PgStore) PutRegionalRuntimeConsent(ctx context.Context, c runtimequota.Consent) (RegionalRuntimeConsent, error) {
	if c.Validate() != nil {
		return RegionalRuntimeConsent{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RegionalRuntimeConsent{}, runtimeConsentError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := &sqlc.Queries{}
	if err = q.LockRegionalRuntimeQuotaJournal(ctx, tx, uuidToPgtype(c.Binding.RegionalAppID)); err != nil {
		return RegionalRuntimeConsent{}, runtimeConsentError(err)
	}
	if err = lockRuntimeConsentOwner(ctx, tx, q, c.Binding); err != nil {
		return RegionalRuntimeConsent{}, runtimeConsentError(err)
	}
	old, err := readRuntimeConsent(ctx, tx, c.ID)
	if err == nil {
		if old.Consent != c {
			return RegionalRuntimeConsent{}, ErrConflict
		}
		return old, runtimeConsentError(tx.Commit(ctx))
	}
	if !errors.Is(err, ErrNotFound) {
		return RegionalRuntimeConsent{}, err
	}
	if err = runtimeConsentExpiry(c, time.Now()); err != nil {
		return RegionalRuntimeConsent{}, err
	}
	raw, err := json.Marshal(c.Binding)
	if err != nil {
		return RegionalRuntimeConsent{}, err
	}
	row, err := q.InsertRegionalRuntimeConsent(ctx, tx, sqlc.InsertRegionalRuntimeConsentParams{ID: uuidToPgtype(c.ID), AppID: uuidToPgtype(c.Binding.RegionalAppID), AccountID: uuidToPgtype(c.Binding.RegionalAccountID), CellID: uuidToPgtype(c.Binding.CellID), IssuerID: uuidToPgtype(c.IssuerID), GlobalAppID: uuidToPgtype(c.GlobalAppID), OwnerID: uuidToPgtype(c.OwnerID), Binding: raw, ExpiresAt: pgtypeTimestamptzFromTime(time.UnixMicro(c.ExpiresAtMicros))})
	if err != nil {
		return RegionalRuntimeConsent{}, runtimeConsentError(err)
	}
	result, err := runtimeConsentRow(row)
	if err != nil {
		return RegionalRuntimeConsent{}, err
	}
	return result, runtimeConsentError(tx.Commit(ctx))
}

func runtimeConsentOwnerMatches(c RegionalRuntimeConsent, owner, cell, app string) bool {
	return c.Consent.Binding.RegionalAccountID == regionalQuotaCanonicalID(owner) && c.Consent.Binding.CellID == cell && c.Consent.Binding.RegionalAppID == app
}

func (s *PgStore) RegionalRuntimeConsent(ctx context.Context, owner, cell, app, id string) (RegionalRuntimeConsent, error) {
	if !validRegionalReleaseIDs(cell, app, id) || regionalQuotaCanonicalID(owner) == "" {
		return RegionalRuntimeConsent{}, ErrInvalidArgument
	}
	c, err := readRuntimeConsent(ctx, s.pool, id)
	if err == nil && !runtimeConsentOwnerMatches(c, owner, cell, app) {
		err = ErrNotFound
	}
	if err != nil {
		return RegionalRuntimeConsent{}, err
	}
	return c, nil
}

func (s *PgStore) RevokeRegionalRuntimeConsent(ctx context.Context, owner, cell, app, id string) (RegionalRuntimeConsent, error) {
	if !validRegionalReleaseIDs(cell, app, id) || regionalQuotaCanonicalID(owner) == "" {
		return RegionalRuntimeConsent{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RegionalRuntimeConsent{}, runtimeConsentError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := &sqlc.Queries{}
	if err = q.LockRegionalRuntimeQuotaJournal(ctx, tx, uuidToPgtype(app)); err != nil {
		return RegionalRuntimeConsent{}, runtimeConsentError(err)
	}
	row, err := q.RevokeRegionalRuntimeConsent(ctx, tx, sqlc.RevokeRegionalRuntimeConsentParams{ID: uuidToPgtype(id), AppID: uuidToPgtype(app), CellID: uuidToPgtype(cell), AccountID: uuidToPgtype(owner)})
	if err != nil {
		return RegionalRuntimeConsent{}, runtimeConsentError(err)
	}
	c, err := runtimeConsentRow(row)
	if err != nil {
		return RegionalRuntimeConsent{}, err
	}
	return c, runtimeConsentError(tx.Commit(ctx))
}
