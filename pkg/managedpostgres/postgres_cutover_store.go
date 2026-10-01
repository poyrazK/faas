package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ CutoverStore = (*PostgresStore)(nil)

func cutoverUUID(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }
func cutoverFromRow(r sqlc.ManagedPostgresCutover) Cutover {
	return Cutover{ID: cutoverUUID(r.ID), AccountID: cutoverUUID(r.AccountID), AppID: cutoverUUID(r.AppID), Scope: r.Scope,
		Source: Database{ID: cutoverUUID(r.SourceDatabaseID), AccountID: cutoverUUID(r.AccountID), BackendID: r.SourceBackendID, BackendFingerprint: r.SourceBackendFingerprint, ProviderResourceID: r.SourceResourceID, DesiredGeneration: r.SourceGeneration},
		Target: Database{ID: cutoverUUID(r.TargetDatabaseID), AccountID: cutoverUUID(r.AccountID), BackendID: r.TargetBackendID, BackendFingerprint: r.TargetBackendFingerprint, ProviderResourceID: r.TargetResourceID, DesiredGeneration: r.TargetGeneration},
		State:  CutoverState(r.State), LastErrorCode: r.LastErrorCode.String, LeaseToken: r.LeaseToken.String, LeaseUntil: bindingDeliveryTime(r.LeaseUntil), AttemptCount: r.AttemptCount, RetryAt: r.RetryAt.Time, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time, VerifiedAt: bindingDeliveryTime(r.VerifiedAt)}
}
func loadCutoverCredentials(ctx context.Context, db sqlc.DBTX, c Cutover) (Cutover, error) {
	rows, err := sqlc.New().ListManagedPostgresCutoverCredentials(ctx, db, c.ID)
	if err != nil {
		return Cutover{}, mapPostgresError(err)
	}
	for _, r := range rows {
		c.Credentials = append(c.Credentials, CutoverCredential{ID: cutoverUUID(r.ID), SourceBindingID: cutoverUUID(r.SourceBindingID), SourceCredentialGeneration: r.SourceCredentialGeneration, EnvironmentKey: r.EnvironmentKey, Access: CredentialAccess(r.Access), State: r.State, VerifiedAt: bindingDeliveryTime(r.VerifiedAt), Sealed: SealedCredential{ProviderIdentityID: r.ProviderIdentityID.String, Ref: r.CredentialRef.String, Ciphertext: r.Ciphertext, Kid: r.Kid.String, ValueHash: r.ValueHash.String}})
	}
	return c, nil
}
func (s *PostgresStore) GetCutover(ctx context.Context, account, id string) (Cutover, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Cutover{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	r, err := sqlc.New().GetManagedPostgresCutover(ctx, tx, sqlc.GetManagedPostgresCutoverParams{AccountID: account, ID: id})
	if err != nil {
		return Cutover{}, mapPostgresError(err)
	}
	c, err := loadCutoverCredentials(ctx, tx, cutoverFromRow(r))
	if err != nil {
		return Cutover{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Cutover{}, mapPostgresError(err)
	}
	return c, nil
}
func (s *PostgresStore) ReserveCutover(ctx context.Context, request PrepareCutoverRequest, now time.Time) (Cutover, bool, error) {
	if !validPrepareCutover(request, now) {
		return Cutover{}, false, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Cutover{}, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	accountStatus, err := q.LockManagedPostgresCutoverAccount(ctx, tx, request.AccountID)
	if err != nil {
		return Cutover{}, false, mapPostgresError(err)
	}
	if accountStatus == "deleted_pending" {
		return Cutover{}, false, ErrConflict
	}
	app, err := q.LockManagedPostgresCutoverApp(ctx, tx, request.AppID)
	if err != nil {
		return Cutover{}, false, mapPostgresError(err)
	}
	if app.AccountID != request.AccountID {
		return Cutover{}, false, ErrNotFound
	}
	if app.Status == "deleted" {
		return Cutover{}, false, ErrConflict
	}
	rows, err := q.LockManagedPostgresCutoverDatabases(ctx, tx, []string{request.SourceDatabaseID, request.TargetDatabaseID})
	if err != nil {
		return Cutover{}, false, mapPostgresError(err)
	}
	if len(rows) != 2 {
		return Cutover{}, false, ErrNotFound
	}
	var source, target sqlc.ManagedPostgresDatabase
	for _, r := range rows {
		if cutoverUUID(r.AccountID) != request.AccountID {
			return Cutover{}, false, ErrNotFound
		}
		if cutoverUUID(r.ID) == request.SourceDatabaseID {
			source = r
		} else {
			target = r
		}
	}
	existing, err := q.GetManagedPostgresCutover(ctx, tx, sqlc.GetManagedPostgresCutoverParams{AccountID: request.AccountID, ID: request.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, err = q.GetActiveManagedPostgresCutover(ctx, tx, sqlc.GetActiveManagedPostgresCutoverParams{AccountID: request.AccountID, AppID: request.AppID, Scope: request.Scope})
	}
	if err == nil {
		c := cutoverFromRow(existing)
		if !sameCutoverRequest(c, request) {
			return Cutover{}, false, ErrConflict
		}
		c, err = loadCutoverCredentials(ctx, tx, c)
		if err != nil {
			return Cutover{}, false, err
		}
		return c, false, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Cutover{}, false, mapPostgresError(err)
	}
	if source.State != "ready" || target.State != "ready" || !source.ProviderResourceID.Valid || !target.ProviderResourceID.Valid || source.CutoverID.Valid || target.CutoverID.Valid || !target.RestoreSourceDatabaseID.Valid || target.RestoreSourceDatabaseID != source.ID || target.RestoreSourceResourceID.String != source.ProviderResourceID.String {
		return Cutover{}, false, ErrConflict
	}
	targetBindings, err := q.CountManagedPostgresCutoverTargetBindings(ctx, tx, request.TargetDatabaseID)
	if err != nil {
		return Cutover{}, false, mapPostgresError(err)
	}
	if targetBindings != 0 {
		return Cutover{}, false, ErrConflict
	}
	bindings, err := q.LockManagedPostgresCutoverBindings(ctx, tx, sqlc.LockManagedPostgresCutoverBindingsParams{AccountID: request.AccountID, DatabaseID: request.SourceDatabaseID, AppID: request.AppID, Scope: request.Scope})
	if err != nil {
		return Cutover{}, false, mapPostgresError(err)
	}
	if len(bindings) == 0 {
		return Cutover{}, false, ErrConflict
	}
	for _, b := range bindings {
		if b.State != "ready" || b.RotationPreviousGeneration.Int64 != 0 || b.CutoverID.Valid || b.LeaseUntil.Valid {
			return Cutover{}, false, ErrConflict
		}
	}
	err = q.InsertManagedPostgresCutover(ctx, tx, sqlc.InsertManagedPostgresCutoverParams{ID: request.ID, AccountID: request.AccountID, AppID: request.AppID, Scope: request.Scope, SourceID: request.SourceDatabaseID, TargetID: request.TargetDatabaseID, SourceBackend: source.BackendID, SourceFingerprint: source.BackendFingerprint, SourceResource: source.ProviderResourceID.String, SourceGeneration: source.DesiredGeneration, TargetBackend: target.BackendID, TargetFingerprint: target.BackendFingerprint, TargetResource: target.ProviderResourceID.String, TargetGeneration: target.DesiredGeneration, Now: healthTimestamp(now)})
	if err != nil {
		return Cutover{}, false, mapPostgresError(err)
	}
	n, err := q.PinManagedPostgresCutoverDatabases(ctx, tx, sqlc.PinManagedPostgresCutoverDatabasesParams{ID: request.ID, DatabaseIds: []string{request.SourceDatabaseID, request.TargetDatabaseID}})
	if err != nil {
		return Cutover{}, false, mapPostgresError(err)
	}
	if n != 2 {
		return Cutover{}, false, ErrConflict
	}
	for _, b := range bindings {
		n, err = q.InsertManagedPostgresCutoverCredential(ctx, tx, sqlc.InsertManagedPostgresCutoverCredentialParams{CutoverID: request.ID, BindingID: cutoverUUID(b.ID), ID: uuid.NewString()})
		if err != nil {
			return Cutover{}, false, mapPostgresError(err)
		}
		if n != 1 {
			return Cutover{}, false, ErrConflict
		}
	}
	r, err := q.GetManagedPostgresCutover(ctx, tx, sqlc.GetManagedPostgresCutoverParams{AccountID: request.AccountID, ID: request.ID})
	if err != nil {
		return Cutover{}, false, mapPostgresError(err)
	}
	c, err := loadCutoverCredentials(ctx, tx, cutoverFromRow(r))
	if err != nil {
		return Cutover{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Cutover{}, false, mapPostgresError(err)
	}
	return c, true, nil
}
func (s *PostgresStore) ClaimCutover(ctx context.Context, account, id, token string, now, until time.Time) (Cutover, error) {
	if token == "" || now.IsZero() || !until.After(now) {
		return Cutover{}, ErrInvalid
	}
	r, err := sqlc.New().ClaimManagedPostgresCutover(ctx, s.pool, sqlc.ClaimManagedPostgresCutoverParams{AccountID: account, ID: id, Token: token, Now: healthTimestamp(now), Until: healthTimestamp(until)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Cutover{}, ErrConflict
	}
	if err != nil {
		return Cutover{}, mapPostgresError(err)
	}
	return loadCutoverCredentials(ctx, s.pool, cutoverFromRow(r))
}
func (s *PostgresStore) SaveCutoverCredential(ctx context.Context, c Cutover, member CutoverCredential, sealed SealedCredential, now time.Time) error {
	if !validSealedCredential(sealed) {
		return ErrInvalid
	}
	return s.finishCutoverStep(ctx, c, member, sealed, false, now)
}
func (s *PostgresStore) RevokeCutoverCredential(ctx context.Context, c Cutover, member CutoverCredential, now time.Time) error {
	return s.finishCutoverStep(ctx, c, member, SealedCredential{}, true, now)
}
func (s *PostgresStore) finishCutoverStep(ctx context.Context, c Cutover, member CutoverCredential, sealed SealedCredential, revoke bool, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	r, err := q.LockManagedPostgresCutoverLease(ctx, tx, sqlc.LockManagedPostgresCutoverLeaseParams{AccountID: c.AccountID, ID: c.ID, Token: c.LeaseToken, Now: healthTimestamp(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return mapPostgresError(err)
	}
	expected := CutoverPreparing
	if revoke {
		expected = CutoverCancelling
	}
	if CutoverState(r.State) != expected {
		return ErrConflict
	}
	var n int64
	if revoke {
		n, err = q.RevokeManagedPostgresCutoverCredential(ctx, tx, sqlc.RevokeManagedPostgresCutoverCredentialParams{ID: member.ID, CutoverID: c.ID})
	} else {
		n, err = q.SaveManagedPostgresCutoverCredential(ctx, tx, sqlc.SaveManagedPostgresCutoverCredentialParams{ID: member.ID, CutoverID: c.ID, ProviderIdentity: sealed.ProviderIdentityID, Ref: sealed.Ref, Ciphertext: sealed.Ciphertext, Kid: sealed.Kid, ValueHash: sealed.ValueHash})
	}
	if err != nil {
		return mapPostgresError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	if err = q.FinishManagedPostgresCutoverStep(ctx, tx, sqlc.FinishManagedPostgresCutoverStepParams{ID: c.ID, Now: healthTimestamp(now)}); err != nil {
		return mapPostgresError(err)
	}
	r, err = q.GetManagedPostgresCutover(ctx, tx, sqlc.GetManagedPostgresCutoverParams{AccountID: c.AccountID, ID: c.ID})
	if err != nil {
		return mapPostgresError(err)
	}
	if r.State == string(CutoverCancelled) {
		if err = q.UnpinManagedPostgresCutoverDatabases(ctx, tx, c.ID); err != nil {
			return mapPostgresError(err)
		}
		if err = q.UnpinManagedPostgresCutoverBindings(ctx, tx, c.ID); err != nil {
			return mapPostgresError(err)
		}
	}
	return mapPostgresError(tx.Commit(ctx))
}
func (s *PostgresStore) ReleaseCutover(ctx context.Context, c Cutover, code string, now, retry time.Time) error {
	if !validErrorCode(code) || code == "" || now.IsZero() || retry.Before(now) {
		return ErrInvalid
	}
	n, err := sqlc.New().ReleaseManagedPostgresCutover(ctx, s.pool, sqlc.ReleaseManagedPostgresCutoverParams{AccountID: c.AccountID, ID: c.ID, Token: c.LeaseToken, Code: code, Now: healthTimestamp(now), RetryAt: healthTimestamp(retry)})
	if err != nil {
		return mapPostgresError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (s *PostgresStore) CancelCutover(ctx context.Context, account, id string, now time.Time) (Cutover, error) {
	if now.IsZero() {
		return Cutover{}, ErrInvalid
	}
	r, err := sqlc.New().CancelManagedPostgresCutover(ctx, s.pool, sqlc.CancelManagedPostgresCutoverParams{AccountID: account, ID: id, Now: healthTimestamp(now)})
	if err != nil {
		return Cutover{}, mapPostgresError(err)
	}
	return loadCutoverCredentials(ctx, s.pool, cutoverFromRow(r))
}

func (s *PostgresStore) RequestCutoverVerification(ctx context.Context, account, id string, now time.Time) (Cutover, error) {
	if now.IsZero() {
		return Cutover{}, ErrInvalid
	}
	q := sqlc.New()
	initial, err := q.GetManagedPostgresCutover(ctx, s.pool, sqlc.GetManagedPostgresCutoverParams{AccountID: account, ID: id})
	if err != nil {
		return Cutover{}, mapPostgresError(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Cutover{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	owner, err := q.LockManagedPostgresCutoverAccount(ctx, tx, account)
	if err != nil {
		return Cutover{}, mapPostgresError(err)
	}
	app, err := q.LockManagedPostgresCutoverApp(ctx, tx, cutoverUUID(initial.AppID))
	if err != nil {
		return Cutover{}, mapPostgresError(err)
	}
	if owner == "deleted_pending" || app.Status == "deleted" || app.AccountID != account {
		return Cutover{}, ErrConflict
	}
	r, err := q.LockManagedPostgresCutoverForVerification(ctx, tx, sqlc.LockManagedPostgresCutoverForVerificationParams{AccountID: account, ID: id})
	if err != nil {
		return Cutover{}, mapPostgresError(err)
	}
	c, err := loadCutoverCredentials(ctx, tx, cutoverFromRow(r))
	if err != nil {
		return Cutover{}, err
	}
	if c.State != CutoverVerifying {
		if (c.State != CutoverPrepared && c.State != CutoverVerified) || c.LeaseUntil.After(now) || len(c.Credentials) == 0 {
			return Cutover{}, ErrConflict
		}
		for _, m := range c.Credentials {
			if m.State != "sealed" {
				return Cutover{}, ErrConflict
			}
		}
		if err := q.RequestManagedPostgresCutoverVerification(ctx, tx, sqlc.RequestManagedPostgresCutoverVerificationParams{ID: id, Now: healthTimestamp(now)}); err != nil {
			return Cutover{}, mapPostgresError(err)
		}
		if err := q.ResetManagedPostgresCutoverVerification(ctx, tx, id); err != nil {
			return Cutover{}, mapPostgresError(err)
		}
		r, err = q.GetManagedPostgresCutover(ctx, tx, sqlc.GetManagedPostgresCutoverParams{AccountID: account, ID: id})
		if err != nil {
			return Cutover{}, mapPostgresError(err)
		}
		c, err = loadCutoverCredentials(ctx, tx, cutoverFromRow(r))
		if err != nil {
			return Cutover{}, err
		}
	}
	return c, mapPostgresError(tx.Commit(ctx))
}

func (s *PostgresStore) SaveCutoverVerification(ctx context.Context, c Cutover, member CutoverCredential, now time.Time) error {
	if now.IsZero() || member.State != "sealed" || !validSealedCredential(member.Sealed) {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	r, err := q.LockManagedPostgresCutoverLease(ctx, tx, sqlc.LockManagedPostgresCutoverLeaseParams{AccountID: c.AccountID, ID: c.ID, Token: c.LeaseToken, Now: healthTimestamp(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return mapPostgresError(err)
	}
	if CutoverState(r.State) != CutoverVerifying {
		return ErrConflict
	}
	sealed := member.Sealed
	n, err := q.SaveManagedPostgresCutoverVerification(ctx, tx, sqlc.SaveManagedPostgresCutoverVerificationParams{Token: c.LeaseToken, CutoverID: c.ID, ID: member.ID, Now: healthTimestamp(now), ProviderIdentity: sealed.ProviderIdentityID, Ref: sealed.Ref, Ciphertext: sealed.Ciphertext, Kid: sealed.Kid, ValueHash: sealed.ValueHash})
	if err != nil {
		return mapPostgresError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	if err := q.FinishManagedPostgresCutoverVerification(ctx, tx, sqlc.FinishManagedPostgresCutoverVerificationParams{ID: c.ID, Now: healthTimestamp(now), Cutoff: healthTimestamp(now.Add(-CutoverVerificationMaxAge))}); err != nil {
		return mapPostgresError(err)
	}
	return mapPostgresError(tx.Commit(ctx))
}
func (s *PostgresStore) DueCutovers(ctx context.Context, include bool, limit int, now time.Time) ([]Cutover, error) {
	if limit < 1 || limit > 100 || now.IsZero() {
		return nil, ErrInvalid
	}
	q := sqlc.New()
	if err := q.CancelDeletedOwnerManagedPostgresCutovers(ctx, s.pool, healthTimestamp(now)); err != nil {
		return nil, mapPostgresError(err)
	}
	rows, err := q.ListDueManagedPostgresCutovers(ctx, s.pool, sqlc.ListDueManagedPostgresCutoversParams{IncludePreparing: include, BatchSize: int32(limit), Now: healthTimestamp(now)})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	out := make([]Cutover, 0, len(rows))
	for _, r := range rows {
		out = append(out, cutoverFromRow(r))
	}
	return out, nil
}
