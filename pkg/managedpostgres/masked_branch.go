package managedpostgres

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/maskedbranch"
)

const DefaultMaskedBranchTTL = 2 * time.Hour

// IsMaskedBranch distinguishes logical copy lineage from a pinned physical
// restore. In particular, a reconciler must never restore the source's WAL.
func (d Database) IsMaskedBranch() bool {
	return d.RestoreSourceDatabaseID != "" && d.RestoreSourceResourceID == "" && d.RestorePointInTime.IsZero()
}

type MaskedBranchCopyRequest struct {
	SourceResourceID string
	TargetResourceID string
	Tables           []api.ManagedPostgresMaskedTable
}

type maskedBranchCopier interface {
	CopyMaskedBranch(context.Context, MaskedBranchCopyRequest) error
}

type maskedBranchPolicy struct {
	sourceResourceID string
	tables           []api.ManagedPostgresMaskedTable
	validated        bool
	previewRevision  int64
}

func MaskedBranchSpec(source Spec) Spec {
	source.Class = ClassDevelopment
	source.Availability = AvailabilitySingleZone
	source.ScaleToZero = true
	return source
}

// CreateMaskedBranch reserves the target and its immutable policy together.
// The lifecycle worker owns provisioning and copying after the request ends.
func (s *Service) CreateMaskedBranch(ctx context.Context, accountID, sourceID string, req api.CreateManagedPostgresMaskedBranchRequest) (Database, error) {
	store, ok := s.store.(*PostgresStore)
	if !ok {
		return Database{}, ErrUnsupported
	}
	if !ValidName(req.Name) || req.TTLSeconds < 0 || req.TTLSeconds > int64(api.ManagedPostgresAllowanceBranchTTLMax/time.Second) {
		return Database{}, ErrInvalid
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	if ttl == 0 {
		ttl = DefaultMaskedBranchTTL
	}
	if !inBranchAllowanceTTL(ttl) || !validBranchTTL(ttl) {
		return Database{}, ErrInvalid
	}
	tables, err := maskedbranch.Normalize(req.Tables)
	if err != nil {
		return Database{}, err
	}
	source, err := customerDatabase(ctx, store, accountID, sourceID)
	if err != nil {
		return Database{}, err
	}
	if !source.Preview.IsZero() {
		return Database{}, ErrPreviewDatabase
	}
	if source.State != StateReady || source.ProviderResourceID == "" {
		return Database{}, ErrConflict
	}
	backend, err := s.registry.Resolve(source.BackendID, source.BackendFingerprint)
	if err != nil {
		return Database{}, err
	}
	if _, ok := backend.Provider.(maskedBranchCopier); !ok || backend.Driver != "firecracker" || source.Spec.RestoreWindowSeconds <= 0 {
		return Database{}, ErrUnsupported
	}
	spec := MaskedBranchSpec(source.Spec)
	if _, err := s.AdmitRestoreReservation(ctx, accountID, RestoreSourceDefinition{
		Spec: spec, BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint,
		ProviderResourceID: source.ProviderResourceID, DataResourceID: source.DataResourceID,
	}); err != nil {
		return Database{}, err
	}
	limit, err := s.branchAllowance(ctx, accountID)
	if err != nil {
		return Database{}, err
	}
	now := s.now()
	policy := maskedBranchPolicy{sourceResourceID: source.ProviderResourceID, tables: tables}
	result, created, err := store.reserve(reserveAsBranch(ctx, limit), Database{
		ID: s.newID(), AccountID: accountID, OrgID: source.OrgID, Name: req.Name, Spec: spec,
		BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint, RestoreSourceDatabaseID: source.ID,
		ExpiresAt: now.Add(ttl), State: StateProvisioning, DesiredGeneration: 1, CreatedAt: now, UpdatedAt: now,
	}, limit, &policy)
	if err != nil || created {
		return result, err
	}
	if !result.IsMaskedBranch() || result.RestoreSourceDatabaseID != source.ID || !branchExpiryMatches(result, ttl) ||
		result.State == StateDeleting || result.State == StateDeleted || !result.ExpiresAt.After(now) {
		return Database{}, ErrConflict
	}
	stored, err := store.maskedBranchPolicy(ctx, result.ID)
	if err != nil {
		return Database{}, err
	}
	if policy.sourceResourceID != stored.sourceResourceID || !reflect.DeepEqual(policy.tables, stored.tables) {
		return Database{}, ErrConflict
	}
	return result, nil
}

func (s *PostgresStore) maskedBranchPolicy(ctx context.Context, id string) (maskedBranchPolicy, error) {
	var p maskedBranchPolicy
	err := s.catalog.QueryRow(ctx, `SELECT source_resource_id, policy, validated_at IS NOT NULL, COALESCE(preview_policy_revision, 0)
		FROM native_postgres_masked_branches WHERE database_id = $1::uuid`, id).Scan(&p.sourceResourceID, &p.tables, &p.validated, &p.previewRevision)
	return p, mapPostgresError(err)
}

func (s *Service) validateMaskedBranchCopy(ctx context.Context, target Database, observed ObservedDatabase, backend Backend) error {
	if observed.RestoreLineage != nil || observed.ProviderResourceID != target.ID || observed.DataResourceID != target.ID {
		return ErrConflict
	}
	store, ok := s.store.(*PostgresStore)
	if !ok {
		return ErrUnsupported
	}
	policy, err := store.maskedBranchPolicy(ctx, target.ID)
	if err != nil || policy.sourceResourceID == target.ID {
		return ErrUnavailable
	}
	isPreview := !target.Preview.IsZero()
	if isPreview != (policy.previewRevision > 0) || isPreview && target.Preview.SourceDatabaseID != target.RestoreSourceDatabaseID {
		return ErrConflict
	}
	if !isPreview && !target.ExpiresAt.After(s.now()) {
		return ErrConflict
	}
	if policy.validated {
		return nil
	}
	source, err := store.Get(ctx, target.AccountID, target.RestoreSourceDatabaseID)
	if err != nil {
		return err
	}
	if source.ProviderResourceID != policy.sourceResourceID || source.OrgID != target.OrgID || source.BackendID != target.BackendID ||
		source.BackendFingerprint != target.BackendFingerprint || source.Spec.PostgresMajor != target.Spec.PostgresMajor {
		return ErrConflict
	}
	if source.State != StateReady {
		return ErrUnavailable
	}
	copier, ok := backend.Provider.(maskedBranchCopier)
	if !ok || backend.Driver != "firecracker" {
		return ErrUnsupported
	}
	if err := copier.CopyMaskedBranch(ctx, MaskedBranchCopyRequest{
		SourceResourceID: policy.sourceResourceID, TargetResourceID: target.ID, Tables: policy.tables,
	}); err != nil {
		return err
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	return store.recordMaskedBranchValidation(finish, target)
}

func (s *PostgresStore) recordMaskedBranchValidation(ctx context.Context, d Database) error {
	tx, err := s.catalog.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var leaseUntil, expiresAt pgtype.Timestamptz
	var isPreview bool
	err = tx.QueryRow(ctx, `SELECT lease_until, expires_at, preview_source_database_id IS NOT NULL FROM managed_postgres_databases
		WHERE id = $1::uuid AND state = 'provisioning' AND lease_token = $2 AND provider_resource_id = id::text
		FOR UPDATE`, d.ID, d.LeaseToken).Scan(&leaseUntil, &expiresAt, &isPreview)
	if err != nil {
		return mapPostgresError(err)
	}
	if !leaseUntil.Valid || !isPreview && !expiresAt.Valid {
		return ErrUnavailable
	}
	tag, err := tx.Exec(ctx, `UPDATE native_postgres_masked_branches SET validated_at = COALESCE(validated_at, clock_timestamp())
		WHERE database_id = $1::uuid AND clock_timestamp() < $2
			AND ($3::timestamptz IS NULL OR clock_timestamp() < $3)
			AND (COALESCE(preview_policy_revision, 0) > 0) = $4`, d.ID, leaseUntil, expiresAt, isPreview)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrUnavailable
	}
	return tx.Commit(ctx)
}

const (
	maskedPolicyUnsupported = "masked_policy_unsupported"
	maskedCopyLimit         = "masked_copy_limit"
	maskedValidationFailed  = "masked_copy_validation_failed"
)

func (s *Service) releaseMaskedBranchError(ctx context.Context, target Database, err error) error {
	var reason string
	switch {
	case errors.Is(err, ErrUnsupported), errors.Is(err, ErrInvalid):
		reason = maskedPolicyUnsupported
	case errors.Is(err, ErrQuotaExceeded):
		reason = maskedCopyLimit
	case errors.Is(err, ErrConflict):
		reason = maskedValidationFailed
	default:
		return s.releaseProviderError(ctx, target, StateProvisioning, ErrUnavailable)
	}
	return s.releaseKnownError(ctx, target, StateFailed, reason, err, MaxBranchTTL)
}
