package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectStorageCustomerUsageV2Store = (*PgStore)(nil)

const objectCustomerUsageV2Columns = `backend_fingerprint, source, version, coverage_end, observed_at, evidence_digest, stored_byte_hours, read_operations, write_operations, egress_bytes`

func scanObjectCustomerUsageV2(row pgx.Row, accountID, backendID string, periodStart time.Time) (api.ObjectStorageCustomerUsageReportV2, error) {
	r := api.ObjectStorageCustomerUsageReportV2{AccountID: accountID, BackendID: backendID, PeriodStart: periodStart}
	var egress pgtype.Int8
	var version int16
	if err := row.Scan(&r.BackendFingerprint, &r.Source, &version, &r.CoverageEnd, &r.ObservedAt, &r.EvidenceDigest, &r.StoredByteHours, &r.ReadOperations, &r.WriteOperations, &egress); err != nil {
		return api.ObjectStorageCustomerUsageReportV2{}, err
	}
	r.Version = int(version)
	if egress.Valid {
		r.EgressBytes = &egress.Int64
	}
	return normalizeObjectCustomerUsageV2(r), nil
}

func (s *PgStore) RecordObjectCustomerUsageV2(ctx context.Context, report api.ObjectStorageCustomerUsageReportV2) error {
	report = normalizeObjectCustomerUsageV2(report)
	if !validObjectCustomerUsageV2(report, time.Now().UTC()) {
		return ErrConflict
	}
	id, _ := uuid.Parse(report.AccountID)
	report.AccountID = id.String()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err := q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(report.AccountID)); err != nil {
		return mapErr(err)
	}
	args := []any{mustPgUUID(report.AccountID), report.BackendID, objectUsageTime(report.PeriodStart)}
	exact, err := scanObjectCustomerUsageV2(tx.QueryRow(ctx,
		`SELECT `+objectCustomerUsageV2Columns+` FROM object_storage_customer_usage_reports_v2 WHERE account_id=$1 AND backend_id=$2 AND period_start=$3 AND observed_at=$4`,
		append(args, objectUsageTime(report.ObservedAt))...), report.AccountID, report.BackendID, report.PeriodStart)
	if err == nil {
		if sameObjectCustomerUsageV2(exact, report) {
			return tx.Commit(ctx)
		}
		return ErrConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	rows, err := q.ObjectUsageBuckets(ctx, tx, mustPgUUID(report.AccountID))
	if err != nil {
		return err
	}
	snapshot := ObjectUsageSnapshot{}
	for _, row := range rows {
		snapshot.Buckets = append(snapshot.Buckets, ObjectBucketUsage{Bucket: ObjectBucket{
			BackendID: row.BackendID, BackendFingerprint: row.BackendFingerprint,
			State: row.State, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		}})
	}
	if !objectCustomerUsageV2Placement(snapshot, report) {
		return ErrNotFound
	}
	latest, err := scanObjectCustomerUsageV2(tx.QueryRow(ctx,
		`SELECT `+objectCustomerUsageV2Columns+` FROM object_storage_customer_usage_reports_v2 WHERE account_id=$1 AND backend_id=$2 AND period_start=$3 ORDER BY observed_at DESC LIMIT 1`,
		args...), report.AccountID, report.BackendID, report.PeriodStart)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && !objectCustomerUsageV2Advances(latest, report) {
		return ErrConflict
	}
	var egress any
	if report.EgressBytes != nil {
		egress = *report.EgressBytes
	}
	_, err = tx.Exec(ctx, `INSERT INTO object_storage_customer_usage_reports_v2
		(account_id, backend_id, backend_fingerprint, source, version, period_start, coverage_end, observed_at, evidence_digest, stored_byte_hours, read_operations, write_operations, egress_bytes)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		mustPgUUID(report.AccountID), report.BackendID, report.BackendFingerprint, report.Source, report.Version,
		objectUsageTime(report.PeriodStart), objectUsageTime(report.CoverageEnd), objectUsageTime(report.ObservedAt), report.EvidenceDigest,
		report.StoredByteHours, report.ReadOperations, report.WriteOperations, egress)
	if err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

func (s *PgStore) LatestObjectCustomerUsageV2(ctx context.Context, accountID, backendID string, periodStart time.Time) (api.ObjectStorageCustomerUsageReportV2, error) {
	id, err := uuid.Parse(accountID)
	if err != nil || backendID == "" || !periodStart.Equal(ObjectStoragePeriod(periodStart)) {
		return api.ObjectStorageCustomerUsageReportV2{}, ErrNotFound
	}
	periodStart = periodStart.UTC()
	r, err := scanObjectCustomerUsageV2(s.pool.QueryRow(ctx,
		`SELECT `+objectCustomerUsageV2Columns+` FROM object_storage_customer_usage_reports_v2 WHERE account_id=$1 AND backend_id=$2 AND period_start=$3 ORDER BY observed_at DESC LIMIT 1`,
		mustPgUUID(id.String()), backendID, objectUsageTime(periodStart)), id.String(), backendID, periodStart)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ObjectStorageCustomerUsageReportV2{}, ErrNotFound
	}
	return r, err
}
