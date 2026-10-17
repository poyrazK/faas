package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ CutoverSourceFenceStore = (*PostgresStore)(nil)

func mapCutoverSourceFenceError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "55000" {
		return ErrConflict
	}
	return mapPostgresError(err)
}

func sourceFenceFromRow(r sqlc.GetManagedPostgresCutoverSourceFenceRow) (CutoverSourceFence, error) {
	f := CutoverSourceFence{CutoverID: cutoverUUID(r.CutoverID), AccountID: r.AccountID, AppID: r.AppID, OwnerToken: cutoverUUID(r.OwnerToken),
		Source: Database{ID: cutoverUUID(r.SourceDatabaseID), AccountID: r.AccountID, BackendID: r.BackendID, BackendFingerprint: r.BackendFingerprint,
			ProviderResourceID: r.SourceProviderResourceID, DataResourceID: r.SourceDataResourceID, DesiredGeneration: r.SourceGeneration, State: StateReady},
		OwnerNodeID: cutoverUUID(r.OwnerNodeID), OwnerRevision: r.OwnerRevision, AdmissionFencedAt: r.AdmissionFencedAt.Time,
		RuntimeFingerprint: r.RuntimeFingerprint, CatalogFingerprint: r.CatalogFingerprint, CatalogInvalidatedAt: bindingDeliveryTime(r.CatalogInvalidatedAt), State: r.State, DatabaseNames: slices.Clone(r.DatabaseNames),
		Maintenance: CheckpointMaintenance{OwnerToken: cutoverUUID(r.MaintenanceID), SourceResourceID: r.SourceDataResourceID,
			State: "reserved", OwnerOID: uint32(r.OwnerOid.Int64), DatabaseOID: uint32(r.DatabaseOid.Int64)},
		MaintenanceStage: r.MaintenanceState, CloseRequestedAt: bindingDeliveryTime(r.CloseRequestedAt), ObservedAt: bindingDeliveryTime(r.ObservedAt),
		LastErrorCode: r.LastErrorCode, RemoteTerminalState: r.RemoteTerminalState.String, RemoteReleasedAt: bindingDeliveryTime(r.RemoteReleasedAt),
		LeaseToken: r.LeaseToken.String, LeaseUntil: bindingDeliveryTime(r.LeaseUntil), AttemptCount: r.AttemptCount,
		RetryAt: r.RetryAt.Time, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
	if f.MaintenanceStage == "ready" {
		f.Maintenance.State = "ready"
	}
	if err := json.Unmarshal(r.SourceSpec, &f.Source.Spec); err != nil {
		return CutoverSourceFence{}, ErrConflict
	}
	if len(r.Closure) > 0 {
		if err := json.Unmarshal(r.Closure, &f.Observation); err != nil || validateSourceFenceObservation(f) != nil {
			return CutoverSourceFence{}, ErrConflict
		}
	}
	return f, nil
}

func readSourceFence(ctx context.Context, db sqlc.DBTX, account, id string) (CutoverSourceFence, error) {
	r, err := sqlc.New().GetManagedPostgresCutoverSourceFence(ctx, db, sqlc.GetManagedPostgresCutoverSourceFenceParams{AccountID: account, ID: id})
	if err != nil {
		return CutoverSourceFence{}, mapCutoverSourceFenceError(err)
	}
	return sourceFenceFromRow(r)
}

func (s *PostgresStore) GetCutoverSourceFence(ctx context.Context, account, id string) (CutoverSourceFence, error) {
	return readSourceFence(ctx, s.pool, account, id)
}

// Mutating paths keep the existing account -> app -> cutover lock order. The
// app lock protects the runtime read from concurrent ownership/receipt changes.
func lockSourceFenceCutover(ctx context.Context, tx pgx.Tx, account, id string) (Cutover, string, error) {
	q := sqlc.New()
	initial, err := q.GetManagedPostgresCutover(ctx, tx, sqlc.GetManagedPostgresCutoverParams{AccountID: account, ID: id})
	if err != nil {
		return Cutover{}, "", mapCutoverSourceFenceError(err)
	}
	accountStatus, err := q.LockManagedPostgresCutoverAccount(ctx, tx, account)
	if err != nil {
		return Cutover{}, "", mapCutoverSourceFenceError(err)
	}
	if _, err = q.LockManagedPostgresCutoverAdmissionApp(ctx, tx, cutoverUUID(initial.AppID)); err != nil {
		return Cutover{}, "", mapCutoverSourceFenceError(err)
	}
	c, err := q.LockManagedPostgresCutoverForVerification(ctx, tx, sqlc.LockManagedPostgresCutoverForVerificationParams{AccountID: account, ID: id})
	if err != nil {
		return Cutover{}, "", mapCutoverSourceFenceError(err)
	}
	out, err := loadCutoverCredentials(ctx, tx, cutoverFromRow(c))
	return out, accountStatus, err
}

func sourceFenceForwardInputs(ctx context.Context, tx pgx.Tx, c Cutover, expected *CutoverSourceFence, now time.Time) (Database, sqlc.ReadManagedPostgresDrainPlanOwnerRow, CutoverPreflight, error) {
	q := sqlc.New()
	var source Database
	owner, err := q.ReadManagedPostgresDrainPlanOwner(ctx, tx, c.AppID)
	if err != nil {
		return source, owner, CutoverPreflight{}, mapCutoverSourceFenceError(err)
	}
	p, err := readCutoverPreflight(ctx, tx, c, now)
	if err != nil {
		return source, owner, p, err
	}
	rows, err := q.ReadManagedPostgresPreflightDatabases(ctx, tx, []string{c.Source.ID})
	if err != nil || len(rows) != 1 {
		return source, owner, p, ErrConflict
	}
	source = databaseFromSQL(rows[0])
	n, err := q.CountManagedPostgresCutoverSourceBindings(ctx, tx, c.Source.ID)
	if err != nil {
		return source, owner, p, mapCutoverSourceFenceError(err)
	}
	if c.State != CutoverVerified && c.State != CutoverVerifying || owner.Status != "active" && owner.Status != "evicted_cold" ||
		!p.SourceMatches || !p.TargetMatches || !p.BindingsMatch || n != int64(len(c.Credentials)) ||
		source.DataResourceID == "" || !owner.NodeID.Valid || uuid.UUID(owner.NodeID.Bytes) == uuid.Nil ||
		cutoverUUID(owner.ManagedPostgresAdmissionCutoverID) != c.ID || !owner.ManagedPostgresAdmissionFencedAt.Valid ||
		p.RuntimeDrain == nil || p.RuntimeDrain.State != "catalog_retired" {
		return source, owner, p, ErrConflict
	}
	if expected != nil && (source.DataResourceID != expected.Source.DataResourceID || source.Spec != expected.Source.Spec ||
		source.DesiredGeneration != expected.Source.DesiredGeneration || source.BackendID != expected.Source.BackendID ||
		source.BackendFingerprint != expected.Source.BackendFingerprint || source.ProviderResourceID != expected.Source.ProviderResourceID ||
		cutoverUUID(owner.NodeID) != expected.OwnerNodeID || owner.ScalingPolicyRevision != expected.OwnerRevision ||
		!owner.ManagedPostgresAdmissionFencedAt.Time.Equal(expected.AdmissionFencedAt)) {
		return source, owner, p, ErrConflict
	}
	if (expected == nil || expected.State == "pending") && !p.VerificationFresh {
		return source, owner, p, ErrUnavailable
	}
	return source, owner, p, nil
}

func (s *PostgresStore) ReserveCutoverSourceFence(ctx context.Context, account, id string, now time.Time) (CutoverSourceFence, error) {
	if now.IsZero() {
		return CutoverSourceFence{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CutoverSourceFence{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c, accountStatus, err := lockSourceFenceCutover(ctx, tx, account, id)
	if err != nil {
		return CutoverSourceFence{}, err
	}
	f, err := readSourceFence(ctx, tx, account, id)
	if err == nil {
		return f, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return f, err
	}
	if accountStatus == "deleted_pending" {
		return f, ErrConflict
	}
	d, owner, p, err := sourceFenceForwardInputs(ctx, tx, c, nil, now)
	if err != nil {
		return f, err
	}
	q := sqlc.New()
	m, err := q.GetManagedPostgresCutoverSourceMaintenance(ctx, tx, d.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = q.InsertManagedPostgresCutoverSourceMaintenance(ctx, tx, sqlc.InsertManagedPostgresCutoverSourceMaintenanceParams{
			ID: uuid.NewString(), SourceID: d.ID, CutoverID: id, BackendID: d.BackendID, Fingerprint: d.BackendFingerprint, ProviderID: d.ProviderResourceID, DataID: d.DataResourceID})
		if err == nil {
			m, err = q.GetManagedPostgresCutoverSourceMaintenance(ctx, tx, d.ID)
		}
	}
	if err != nil {
		return f, mapCutoverSourceFenceError(err)
	}
	if m.BackendID != d.BackendID || m.BackendFingerprint != d.BackendFingerprint || m.SourceProviderResourceID != d.ProviderResourceID || m.SourceDataResourceID != d.DataResourceID {
		return f, ErrConflict
	}
	spec, err := json.Marshal(d.Spec)
	if err != nil {
		return f, ErrInvalid
	}
	err = q.InsertManagedPostgresCutoverSourceFence(ctx, tx, sqlc.InsertManagedPostgresCutoverSourceFenceParams{
		ID: id, OwnerToken: uuid.NewString(), MaintenanceID: cutoverUUID(m.ID), SourceID: d.ID, BackendID: d.BackendID,
		Fingerprint: d.BackendFingerprint, ProviderID: d.ProviderResourceID, DataID: d.DataResourceID, Generation: d.DesiredGeneration,
		Spec: spec, NodeID: cutoverUUID(owner.NodeID), OwnerRevision: owner.ScalingPolicyRevision, FencedAt: owner.ManagedPostgresAdmissionFencedAt,
		RuntimeFingerprint: p.RuntimeDrainFingerprint, Now: healthTimestamp(now)})
	if err != nil {
		return f, mapCutoverSourceFenceError(err)
	}
	f, err = readSourceFence(ctx, tx, account, id)
	if err != nil {
		return f, err
	}
	return f, mapCutoverSourceFenceError(tx.Commit(ctx))
}

func (s *PostgresStore) ClaimCutoverSourceFence(ctx context.Context, account, id, token string, now, until time.Time) (CutoverSourceFence, error) {
	if token == "" || now.IsZero() || !until.After(now) {
		return CutoverSourceFence{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CutoverSourceFence{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	c, accountStatus, err := lockSourceFenceCutover(ctx, tx, account, id)
	if err != nil {
		return CutoverSourceFence{}, err
	}
	q := sqlc.New()
	if _, err = q.LockManagedPostgresCutoverSourceFence(ctx, tx, id); err != nil {
		return CutoverSourceFence{}, mapCutoverSourceFenceError(err)
	}
	f, err := readSourceFence(ctx, tx, account, id)
	if err != nil || f.State == "released" {
		return f, err
	}
	waitForVerification := false
	if accountStatus == "deleted_pending" {
		f.State = "abandoning"
	} else if f.State != "abandoning" {
		_, _, _, err = sourceFenceForwardInputs(ctx, tx, c, &f, now)
		if errors.Is(err, ErrConflict) {
			f.State = "abandoning"
		} else if errors.Is(err, ErrUnavailable) {
			waitForVerification = true
		} else if err != nil {
			return f, err
		}
	}
	n, err := q.ClaimManagedPostgresCutoverSourceFence(ctx, tx, sqlc.ClaimManagedPostgresCutoverSourceFenceParams{
		ID: id, State: f.State, Token: pgtype.Text{String: token, Valid: true}, Until: healthTimestamp(until)})
	if err != nil {
		return f, mapCutoverSourceFenceError(err)
	}
	if n != 1 {
		return f, ErrConflict
	}
	f, err = readSourceFence(ctx, tx, account, id)
	if err != nil {
		return f, err
	}
	if waitForVerification {
		// Expired verification waits without provider I/O. Persist a bounded
		// retry so an old pending request cannot monopolize the recovery queue.
		n, err = q.SaveManagedPostgresCutoverSourceFence(ctx, tx, sqlc.SaveManagedPostgresCutoverSourceFenceParams{
			ID: id, Token: token, State: f.State, Names: append([]string{}, f.DatabaseNames...),
			CatalogFingerprint:   f.CatalogFingerprint,
			CatalogInvalidatedAt: optionalSourceFenceTime(f.CatalogInvalidatedAt),
			ErrorCode:            "source_fence_verification_required", RetryAt: healthTimestamp(until)})
		if err != nil {
			return f, mapCutoverSourceFenceError(err)
		}
		if n != 1 {
			return f, ErrConflict
		}
		f, err = readSourceFence(ctx, tx, account, id)
		if err != nil {
			return f, err
		}
		if err = tx.Commit(ctx); err != nil {
			return f, mapCutoverSourceFenceError(err)
		}
		return f, ErrUnavailable
	}
	return f, mapCutoverSourceFenceError(tx.Commit(ctx))
}

func (s *PostgresStore) DueCutoverSourceFences(ctx context.Context, includePending bool, limit int, now time.Time) ([]Cutover, error) {
	if limit < 1 || limit > 100 || now.IsZero() {
		return nil, ErrInvalid
	}
	rows, err := sqlc.New().DueManagedPostgresCutoverSourceFences(ctx, s.pool, sqlc.DueManagedPostgresCutoverSourceFencesParams{
		Now: healthTimestamp(now), IncludePending: includePending, BatchSize: int32(limit)})
	if err != nil {
		return nil, mapCutoverSourceFenceError(err)
	}
	out := make([]Cutover, 0, len(rows))
	for _, row := range rows {
		out = append(out, cutoverFromRow(row))
	}
	return out, nil
}
