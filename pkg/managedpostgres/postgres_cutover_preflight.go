package managedpostgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ CutoverPreflightStore = (*PostgresStore)(nil)

func preflightFromRow(r sqlc.ManagedPostgresCutoverPreflight) (CutoverPreflight, error) {
	p := CutoverPreflight{CutoverID: cutoverUUID(r.CutoverID), State: r.State, Revision: r.Revision, BindingIDs: r.BindingIds, Blockers: r.Blockers,
		VerificationFresh: r.VerificationFresh, SourceMatches: r.SourceMatches, TargetMatches: r.TargetMatches, BindingsMatch: r.BindingsMatch,
		CutoverUpdatedAt: bindingDeliveryTime(r.CutoverUpdatedAt), CheckedAt: bindingDeliveryTime(r.CheckedAt), ExpiresAt: bindingDeliveryTime(r.ExpiresAt), CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time,
		RuntimeDrainFingerprint: r.RuntimeDrainFingerprint, SourceSessionsFingerprint: r.SourceSessionsFingerprint}
	if len(r.RuntimeDrain) > 0 {
		if err := json.Unmarshal(r.RuntimeDrain, &p.RuntimeDrain); err != nil {
			return CutoverPreflight{}, ErrConflict
		}
	}
	if len(r.SourceSessions) > 0 {
		if err := json.Unmarshal(r.SourceSessions, &p.SourceSessions); err != nil {
			return CutoverPreflight{}, ErrConflict
		}
	}
	return p, nil
}

func (s *PostgresStore) RequestCutoverPreflight(ctx context.Context, account, id string, now time.Time) (CutoverPreflight, error) {
	if now.IsZero() {
		return CutoverPreflight{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CutoverPreflight{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	c, err := q.LockManagedPostgresCutoverForVerification(ctx, tx, sqlc.LockManagedPostgresCutoverForVerificationParams{AccountID: account, ID: id})
	if err != nil {
		return CutoverPreflight{}, mapPostgresError(err)
	}
	if c.State == string(CutoverCancelling) || CutoverState(c.State).Closed() {
		return CutoverPreflight{}, ErrConflict
	}
	if err = q.RequestManagedPostgresCutoverPreflight(ctx, tx, sqlc.RequestManagedPostgresCutoverPreflightParams{ID: id, Now: healthTimestamp(now)}); err != nil {
		return CutoverPreflight{}, mapPostgresError(err)
	}
	r, err := q.GetManagedPostgresCutoverPreflight(ctx, tx, sqlc.GetManagedPostgresCutoverPreflightParams{AccountID: account, ID: id})
	if err != nil {
		return CutoverPreflight{}, mapPostgresError(err)
	}
	p, err := preflightFromRow(r)
	if err != nil {
		return CutoverPreflight{}, err
	}
	return p, mapPostgresError(tx.Commit(ctx))
}

func (s *PostgresStore) GetCutoverPreflight(ctx context.Context, account, id string) (CutoverPreflight, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return CutoverPreflight{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	r, err := q.GetManagedPostgresCutoverPreflight(ctx, tx, sqlc.GetManagedPostgresCutoverPreflightParams{AccountID: account, ID: id})
	if err != nil {
		return CutoverPreflight{}, mapPostgresError(err)
	}
	p, err := preflightFromRow(r)
	if err != nil {
		return CutoverPreflight{}, err
	}
	if p.State == "blocked" {
		c, err := q.GetManagedPostgresCutover(ctx, tx, sqlc.GetManagedPostgresCutoverParams{AccountID: account, ID: id})
		if err != nil {
			return CutoverPreflight{}, mapPostgresError(err)
		}
		_, fingerprint, err := readPreflightRuntime(ctx, tx, cutoverFromRow(c))
		if err != nil {
			return CutoverPreflight{}, err
		}
		p.RuntimeDrainChanged = p.RuntimeDrainFingerprint == "" || p.RuntimeDrainFingerprint != fingerprint
		_, sourceFingerprint, _, err := readPreflightSourceSessions(ctx, tx, cutoverFromRow(c), time.Now().UTC())
		if err != nil {
			return CutoverPreflight{}, err
		}
		p.SourceSessionsChanged = p.SourceSessionsFingerprint == "" || p.SourceSessionsFingerprint != sourceFingerprint
	}
	return p, mapPostgresError(tx.Commit(ctx))
}

func (s *PostgresStore) EvaluateCutoverPreflight(ctx context.Context, account, id string, now time.Time) (CutoverPreflight, error) {
	if now.IsZero() {
		return CutoverPreflight{}, ErrInvalid
	}
	// The report describes one catalog snapshot. No VM/provider call, admission
	// fence, credential publication or permission to resume is produced here.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return CutoverPreflight{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	r, err := q.LockManagedPostgresCutoverForVerification(ctx, tx, sqlc.LockManagedPostgresCutoverForVerificationParams{AccountID: account, ID: id})
	if err != nil {
		return CutoverPreflight{}, mapPostgresError(err)
	}
	old, err := q.GetManagedPostgresCutoverPreflight(ctx, tx, sqlc.GetManagedPostgresCutoverPreflightParams{AccountID: account, ID: id})
	if err != nil {
		return CutoverPreflight{}, mapPostgresError(err)
	}
	if old.State != "pending" {
		return preflightFromRow(old)
	}
	c, err := loadCutoverCredentials(ctx, tx, cutoverFromRow(r))
	if err != nil {
		return CutoverPreflight{}, err
	}
	p, err := readCutoverPreflight(ctx, tx, c, now)
	if err != nil {
		return CutoverPreflight{}, err
	}
	runtimeJSON, err := json.Marshal(p.RuntimeDrain)
	if err != nil {
		return CutoverPreflight{}, err
	}
	sourceJSON, err := json.Marshal(p.SourceSessions)
	if err != nil {
		return CutoverPreflight{}, err
	}
	row, err := q.FinishManagedPostgresCutoverPreflight(ctx, tx, sqlc.FinishManagedPostgresCutoverPreflightParams{
		ID: id, Revision: old.Revision, BindingIds: p.BindingIDs, Blockers: p.Blockers, VerificationFresh: p.VerificationFresh,
		SourceMatches: p.SourceMatches, TargetMatches: p.TargetMatches, BindingsMatch: p.BindingsMatch,
		CutoverUpdatedAt: healthTimestamp(p.CutoverUpdatedAt), Now: healthTimestamp(now), ExpiresAt: healthTimestamp(p.ExpiresAt),
		RuntimeDrain: runtimeJSON, RuntimeDrainFingerprint: p.RuntimeDrainFingerprint,
		SourceSessions: sourceJSON, SourceSessionsFingerprint: p.SourceSessionsFingerprint})
	if err != nil {
		return CutoverPreflight{}, mapPostgresError(err)
	}
	p, err = preflightFromRow(row)
	if err != nil {
		return CutoverPreflight{}, err
	}
	return p, mapPostgresError(tx.Commit(ctx))
}

func readCutoverPreflight(ctx context.Context, tx pgx.Tx, c Cutover, now time.Time) (CutoverPreflight, error) {
	q := sqlc.New()
	rows, err := q.ReadManagedPostgresPreflightDatabases(ctx, tx, []string{c.Source.ID, c.Target.ID})
	if err != nil {
		return CutoverPreflight{}, mapPostgresError(err)
	}
	var source, target Database
	for _, r := range rows {
		d := databaseFromSQL(r)
		if d.ID == c.Source.ID {
			source = d
		} else {
			target = d
		}
	}
	bs, err := q.ReadManagedPostgresPreflightBindings(ctx, tx, sqlc.ReadManagedPostgresPreflightBindingsParams{DatabaseID: c.Source.ID, AppID: c.AppID, Scope: c.Scope})
	if err != nil {
		return CutoverPreflight{}, mapPostgresError(err)
	}
	bindings := make([]Binding, 0, len(bs))
	for _, b := range bs {
		bindings = append(bindings, bindingFromSQL(b))
	}
	n, err := q.CountManagedPostgresCutoverTargetBindings(ctx, tx, c.Target.ID)
	if err != nil {
		return CutoverPreflight{}, mapPostgresError(err)
	}
	p := evaluateCutoverPreflight(c, source, target, bindings, n > 0, now)
	p.RuntimeDrain, p.RuntimeDrainFingerprint, err = readPreflightRuntime(ctx, tx, c)
	if err != nil {
		return p, err
	}
	var expiry time.Time
	p.SourceSessions, p.SourceSessionsFingerprint, expiry, err = readPreflightSourceSessions(ctx, tx, c, now)
	if !expiry.IsZero() && expiry.Before(p.ExpiresAt) {
		p.ExpiresAt = expiry
	}
	return p, err
}

func (s *PostgresStore) PendingCutoverPreflights(ctx context.Context, limit int) ([]Cutover, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	rows, err := sqlc.New().PendingManagedPostgresCutoverPreflights(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, mapPostgresError(err)
	}
	out := make([]Cutover, 0, len(rows))
	for _, r := range rows {
		out = append(out, cutoverFromRow(r))
	}
	return out, nil
}
