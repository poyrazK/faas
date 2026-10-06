package globaldirectory

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeConsentStore = (*PgStore)(nil)

func directoryRuntimeConsentError(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) {
		switch p.ConstraintName {
		case "global_runtime_consent_unavailable":
			return ErrRuntimeConsentUnavailable
		case "global_runtime_consent_identity":
			return ErrConflict
		case "global_runtime_consent_limit":
			return &RuntimeConsentLimitError{Limit: api.GlobalRuntimeConsentsPerAppMax, Observed: api.GlobalRuntimeConsentsPerAppMax + 1}
		}
	}
	return err
}

func directoryRuntimeConsentRow(row sqlc.GlobalRuntimeConsent) (RuntimeConsentRecord, error) {
	c := runtimequota.Consent{ID: uuidText(row.ID), IssuerID: uuidText(row.IssuerID), GlobalAppID: uuidText(row.GlobalAppID), OwnerID: uuidText(row.OwnerID), ExpiresAtMicros: row.ExpiresAt.Time.UnixMicro()}
	if json.Unmarshal(row.Binding, &c.Binding) != nil || c.Validate() != nil {
		return RuntimeConsentRecord{}, ErrConflict
	}
	return RuntimeConsentRecord{Consent: c, OwnerID: c.OwnerID, Slug: row.GlobalAppSlug, CreatedAt: row.CreatedAt.Time, Status: row.Status, RegionalCreatedAt: quotaTime(row.RegionalCreatedAt), RegionalRevokedAt: quotaTime(row.RegionalRevokedAt)}, nil
}

func readDirectoryRuntimeConsent(ctx context.Context, db sqlc.DBTX, owner, slug, id string) (RuntimeConsentRecord, error) {
	row, err := (&sqlc.Queries{}).ReadGlobalRuntimeConsent(ctx, db, sqlc.ReadGlobalRuntimeConsentParams{ConsentID: idArg(id), OwnerID: idArg(owner), Slug: slug})
	if err != nil {
		return RuntimeConsentRecord{}, err
	}
	return directoryRuntimeConsentRow(row)
}

func checkDirectoryRuntimeConsent(ctx context.Context, db sqlc.DBTX, r RuntimeConsentRecord) error {
	if !r.setupAllowed(time.Now()) {
		return ErrRuntimeConsentUnavailable
	}
	b, err := json.Marshal(r.Consent.Binding)
	if err != nil {
		return err
	}
	return (&sqlc.Queries{}).CheckGlobalRuntimeConsentScope(ctx, db, sqlc.CheckGlobalRuntimeConsentScopeParams{OwnerID: idArg(r.OwnerID), GlobalAppID: idArg(r.Consent.GlobalAppID), Slug: r.Slug, IssuerID: idArg(r.Consent.IssuerID), Binding: b, ExpiresAt: pgtype.Timestamptz{Time: time.UnixMicro(r.Consent.ExpiresAtMicros), Valid: true}})
}

func (s *PgStore) PrepareRuntimeConsent(ctx context.Context, owner, slug string, c runtimequota.Consent) (RuntimeConsentRecord, error) {
	if c.Validate() != nil || !ValidSlug(slug) || runtimeConsentOwner(owner) != c.OwnerID {
		return RuntimeConsentRecord{}, ErrConflict
	}
	var out RuntimeConsentRecord
	err := s.transaction(ctx, "prepare runtime consent", func(tx pgx.Tx) error {
		q := &sqlc.Queries{}
		if err := q.LockGlobalRuntimeConsentJournal(ctx, tx, idArg(c.GlobalAppID)); err != nil {
			return err
		}
		old, err := readDirectoryRuntimeConsent(ctx, tx, owner, slug, c.ID)
		if err == nil {
			if old.Consent != c {
				return ErrConflict
			}
			out = old
			return directoryRuntimeConsentError(checkDirectoryRuntimeConsent(ctx, tx, old))
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		b, err := json.Marshal(c.Binding)
		if err != nil {
			return err
		}
		row, err := q.InsertGlobalRuntimeConsent(ctx, tx, sqlc.InsertGlobalRuntimeConsentParams{ConsentID: idArg(c.ID), IssuerID: idArg(c.IssuerID), GlobalAppID: idArg(c.GlobalAppID), OwnerID: idArg(owner), Slug: slug, Binding: b, ExpiresAt: pgtype.Timestamptz{Time: time.UnixMicro(c.ExpiresAtMicros), Valid: true}})
		if err != nil {
			return directoryRuntimeConsentError(err)
		}
		out, err = directoryRuntimeConsentRow(row)
		return err
	})
	return out, err
}

func (s *PgStore) RuntimeConsent(ctx context.Context, owner, slug, id string) (RuntimeConsentRecord, error) {
	var out RuntimeConsentRecord
	err := s.transaction(ctx, "read runtime consent", func(tx pgx.Tx) error {
		var err error
		out, err = readDirectoryRuntimeConsent(ctx, tx, owner, slug, id)
		return err
	})
	return out, err
}

// All mutations and dispatch checks serialize using the immutable original app,
// then reread the record; removal of the app does not remove the journal lock.
func (s *PgStore) runtimeConsentTransaction(ctx context.Context, owner, slug, id, op string, change func(pgx.Tx, RuntimeConsentRecord) (RuntimeConsentRecord, error)) (RuntimeConsentRecord, error) {
	var out RuntimeConsentRecord
	err := s.transaction(ctx, op, func(tx pgx.Tx) error {
		r, err := readDirectoryRuntimeConsent(ctx, tx, owner, slug, id)
		if err != nil {
			return err
		}
		if err = (&sqlc.Queries{}).LockGlobalRuntimeConsentJournal(ctx, tx, idArg(r.Consent.GlobalAppID)); err != nil {
			return err
		}
		r, err = readDirectoryRuntimeConsent(ctx, tx, owner, slug, id)
		if err != nil {
			return err
		}
		out, err = change(tx, r)
		return directoryRuntimeConsentError(err)
	})
	return out, err
}

func (s *PgStore) CheckRuntimeConsent(ctx context.Context, owner, slug, id string) (RuntimeConsentRecord, error) {
	return s.runtimeConsentTransaction(ctx, owner, slug, id, "check runtime consent", func(tx pgx.Tx, r RuntimeConsentRecord) (RuntimeConsentRecord, error) {
		if r.Status != RuntimeConsentActive {
			return r, ErrRuntimeConsentUnavailable
		}
		return r, checkDirectoryRuntimeConsent(ctx, tx, r)
	})
}

func (s *PgStore) ObserveRuntimeConsent(ctx context.Context, owner, slug, id string, meta state.RegionalRuntimeConsent) (RuntimeConsentRecord, error) {
	return s.runtimeConsentTransaction(ctx, owner, slug, id, "observe runtime consent", func(tx pgx.Tx, r RuntimeConsentRecord) (RuntimeConsentRecord, error) {
		r, err := r.observe(meta)
		if err != nil {
			return r, err
		}
		var revoked pgtype.Timestamptz
		if r.RegionalRevokedAt != nil {
			revoked = pgtype.Timestamptz{Time: *r.RegionalRevokedAt, Valid: true}
		}
		row, err := (&sqlc.Queries{}).ObserveGlobalRuntimeConsent(ctx, tx, sqlc.ObserveGlobalRuntimeConsentParams{ConsentID: idArg(id), OwnerID: idArg(owner), Slug: slug, Status: r.Status, RegionalCreatedAt: pgtype.Timestamptz{Time: *r.RegionalCreatedAt, Valid: true}, RegionalRevokedAt: revoked})
		if err != nil {
			return r, err
		}
		return directoryRuntimeConsentRow(row)
	})
}

func (s *PgStore) BeginRevokeRuntimeConsent(ctx context.Context, owner, slug, id string) (RuntimeConsentRecord, error) {
	return s.runtimeConsentTransaction(ctx, owner, slug, id, "revoke runtime consent", func(tx pgx.Tx, r RuntimeConsentRecord) (RuntimeConsentRecord, error) {
		row, err := (&sqlc.Queries{}).BeginRevokeGlobalRuntimeConsent(ctx, tx, sqlc.BeginRevokeGlobalRuntimeConsentParams{ConsentID: idArg(id), OwnerID: idArg(owner), Slug: slug})
		if err != nil {
			return r, err
		}
		return directoryRuntimeConsentRow(row)
	})
}
