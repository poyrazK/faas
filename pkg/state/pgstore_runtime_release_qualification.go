package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeReleaseQualificationStore = (*PgStore)(nil)

func runtimeQualificationFromRow(q sqlc.RuntimeReleaseQualification) RuntimeReleaseQualification {
	return RuntimeReleaseQualification{ReleaseID: q.ReleaseID, Profile: q.Profile, Architecture: q.Architecture,
		HostID: uuid.UUID(q.HostID.Bytes).String(), KernelBootID: uuid.UUID(q.KernelBootID.Bytes).String(), SourceCommit: q.SourceCommit,
		KernelSHA256: q.KernelSha256, FirecrackerSHA256: q.FirecrackerSha256, ReportSHA256: q.ReportSha256,
		TestMetalSHA256: q.TestMetalSha256, LeakcheckSHA256: q.LeakcheckSha256,
		StartedAt: q.StartedAt.Time, CompletedAt: q.CompletedAt.Time, RecordedAt: q.RecordedAt.Time,
		RevokedAt: q.RevokedAt.Time, RevocationSHA256: q.RevocationSha256.String}
}

func (s *PgStore) RecordRuntimeReleaseQualification(ctx context.Context, q RuntimeReleaseQualification) (RuntimeReleaseQualification, error) {
	q, err := normalizeRuntimeQualificationInput(q)
	if err != nil {
		return RuntimeReleaseQualification{}, err
	}
	if _, err := s.RuntimeReleaseByID(ctx, q.ReleaseID); err != nil {
		return RuntimeReleaseQualification{}, err
	}
	row, err := sqlc.New().RecordRuntimeReleaseQualification(ctx, s.pool, sqlc.RecordRuntimeReleaseQualificationParams{
		ReleaseID: q.ReleaseID, Profile: q.Profile, Architecture: q.Architecture,
		HostID: NewPgtypeUUID(uuid.MustParse(q.HostID)), KernelBootID: NewPgtypeUUID(uuid.MustParse(q.KernelBootID)),
		SourceCommit: q.SourceCommit, KernelSha256: q.KernelSHA256, FirecrackerSha256: q.FirecrackerSHA256,
		ReportSha256: q.ReportSHA256, TestMetalSha256: q.TestMetalSHA256, LeakcheckSha256: q.LeakcheckSHA256,
		StartedAt: pgtype.Timestamptz{Time: q.StartedAt, Valid: true}, CompletedAt: pgtype.Timestamptz{Time: q.CompletedAt, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeReleaseQualification{}, ErrConflict
	}
	return runtimeQualificationFromRow(row), mapErr(err)
}

func (s *PgStore) RuntimeReleaseQualification(ctx context.Context, id string) (RuntimeReleaseQualification, error) {
	if !runtimeSHA.MatchString(id) {
		return RuntimeReleaseQualification{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetRuntimeReleaseQualification(ctx, s.pool, id)
	return runtimeQualificationFromRow(row), mapErr(err)
}

func (s *PgStore) RevokeRuntimeReleaseQualification(ctx context.Context, id, expectedReport, reason string) error {
	if !runtimeSHA.MatchString(id) || !runtimeSHA.MatchString(expectedReport) || !runtimeSHA.MatchString(reason) {
		return ErrInvalidArgument
	}
	_, err := sqlc.New().RevokeRuntimeReleaseQualification(ctx, s.pool, sqlc.RevokeRuntimeReleaseQualificationParams{
		ReleaseID: id, ExpectedReport: expectedReport, Reason: reason,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, readErr := s.RuntimeReleaseQualification(ctx, id); readErr != nil {
			return readErr
		}
		return ErrConflict
	}
	return mapErr(err)
}
