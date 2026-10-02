package state

import (
	"context"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

func (s *PgStore) CaptureRuntimeLogArchiveBinding(ctx context.Context, instanceID, nodeID string) (RuntimeLogArchiveBinding, error) {
	id, err := parsePgUUID(instanceID)
	if err != nil {
		return RuntimeLogArchiveBinding{}, err
	}
	node, err := parsePgUUID(nodeID)
	if err != nil {
		return RuntimeLogArchiveBinding{}, err
	}
	b, err := sqlc.New().CaptureRuntimeLogArchiveBinding(ctx, s.pool, sqlc.CaptureRuntimeLogArchiveBindingParams{InstanceID: id, NodeID: node})
	if err != nil {
		return RuntimeLogArchiveBinding{}, mapErr(err)
	}
	return runtimeLogBindingFromPG(b.InstanceID, b.AppID, b.AccountID, b.DeploymentID, b.ComputeRegion), nil
}
func runtimeLogBindingFromPG(instance string, app, account, dep pgtype.UUID, id string) RuntimeLogArchiveBinding {
	return RuntimeLogArchiveBinding{InstanceID: instance, AppID: pgUUIDString(app), AccountID: pgUUIDString(account), DeploymentID: pgUUIDString(dep), ComputeRegion: id}
}
func runtimeLogDay(day string) (pgtype.Date, error) {
	if !region.ValidRuntimeLogDay(day) {
		return pgtype.Date{}, ErrInvalidArgument
	}
	t, _ := time.Parse("2006-01-02", day)
	return pgtype.Date{Time: t, Valid: true}, nil
}
func (s *PgStore) RuntimeLogArchiveBindingByInstance(ctx context.Context, instanceID string) (RuntimeLogArchiveBinding, error) {
	b, err := sqlc.New().RuntimeLogArchiveBindingByInstance(ctx, s.pool, instanceID)
	if err != nil {
		return RuntimeLogArchiveBinding{}, mapErr(err)
	}
	return runtimeLogBindingFromPG(b.InstanceID, b.AppID, b.AccountID, b.DeploymentID, b.ComputeRegion), nil
}
func (s *PgStore) ReserveRuntimeLogArchive(ctx context.Context, a RuntimeLogArchive) error {
	if !a.ValidReference() {
		return ErrInvalidArgument
	}
	b, err := s.RuntimeLogArchiveBindingByInstance(ctx, a.InstanceID)
	if err != nil {
		return err
	}
	if b != a.RuntimeLogArchiveBinding {
		return ErrInvalidArgument
	}
	day, err := runtimeLogDay(a.Day)
	if err != nil {
		return err
	}
	n, err := sqlc.New().ReserveRuntimeLogArchive(ctx, s.pool, sqlc.ReserveRuntimeLogArchiveParams{InstanceID: a.InstanceID, ArchiveDay: day, StorageKey: a.StorageKey})
	if err != nil {
		return err
	}
	if n != 1 {
		return s.runtimeLogArchiveWriteMiss(ctx, a.InstanceID, a.Day)
	}
	return nil
}
func (s *PgStore) PublishRuntimeLogArchive(ctx context.Context, instanceID, day string, bytes int64, digest string) error {
	return s.PublishRuntimeLogArchiveFragment(ctx, instanceID, day, bytes, digest, "")
}
func (s *PgStore) PublishRuntimeLogArchiveFragment(ctx context.Context, instanceID, day string, bytes int64, digest, fragment string) error {
	if bytes <= 0 || !region.ValidArtifactSHA256(digest) || (fragment != "" && !region.ValidArtifactSHA256(fragment)) {
		return ErrInvalidArgument
	}
	date, err := runtimeLogDay(day)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	n, err := q.PublishRuntimeLogArchive(ctx, tx, sqlc.PublishRuntimeLogArchiveParams{InstanceID: instanceID, ArchiveDay: date, Bytes: bytes, Sha256: digest})
	if err != nil {
		return err
	}
	if n != 1 {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.runtimeLogArchiveWriteMiss(ctx, instanceID, day)
	}
	if fragment != "" {
		if err := q.RecordRuntimeLogArchiveFragment(ctx, tx, sqlc.RecordRuntimeLogArchiveFragmentParams{InstanceID: instanceID, ArchiveDay: date, FragmentSha256: fragment}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *PgStore) RuntimeLogArchiveFragmentPublished(ctx context.Context, instance, day, fragment string) (bool, error) {
	if !region.ValidArtifactSHA256(fragment) {
		return false, ErrInvalidArgument
	}
	date, err := runtimeLogDay(day)
	if err != nil {
		return false, err
	}
	return sqlc.New().RuntimeLogArchiveFragmentPublished(ctx, s.pool, sqlc.RuntimeLogArchiveFragmentPublishedParams{InstanceID: instance, ArchiveDay: date, FragmentSha256: fragment})
}
func (s *PgStore) RuntimeLogArchiveByInstanceDay(ctx context.Context, instanceID, day string) (RuntimeLogArchive, error) {
	date, err := runtimeLogDay(day)
	if err != nil {
		return RuntimeLogArchive{}, err
	}
	if expired, err := s.RuntimeLogArchiveDayExpired(ctx, instanceID, day); err != nil {
		return RuntimeLogArchive{}, err
	} else if expired {
		return RuntimeLogArchive{}, ErrRuntimeLogArchiveExpired
	}
	a, err := sqlc.New().RuntimeLogArchiveByInstanceDay(ctx, s.pool, sqlc.RuntimeLogArchiveByInstanceDayParams{InstanceID: instanceID, ArchiveDay: date})
	if err != nil {
		return RuntimeLogArchive{}, mapErr(err)
	}
	return runtimeLogArchiveFromPG(runtimeLogBindingFromPG(a.InstanceID, a.AppID, a.AccountID, a.DeploymentID, a.ComputeRegion), a.ArchiveDay, a.StorageKey, a.Bytes, a.Sha256, a.PublishedAt), nil
}
func runtimeLogArchiveFromPG(b RuntimeLogArchiveBinding, day pgtype.Date, key string, bytes int64, digest string, at pgtype.Timestamptz) RuntimeLogArchive {
	return RuntimeLogArchive{RuntimeLogArchiveBinding: b, Day: day.Time.Format("2006-01-02"), StorageKey: key, Bytes: bytes, SHA256: digest, PublishedAt: at.Time}
}
func (s *PgStore) ListAppRuntimeLogArchives(ctx context.Context, appID string) ([]RuntimeLogArchive, error) {
	id, err := parsePgUUID(appID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListAppRuntimeLogArchives(ctx, s.pool, id)
	if err != nil {
		return nil, err
	}
	out := make([]RuntimeLogArchive, 0, len(rows))
	for _, a := range rows {
		out = append(out, runtimeLogArchiveFromPG(runtimeLogBindingFromPG(a.InstanceID, a.AppID, a.AccountID, a.DeploymentID, a.ComputeRegion), a.ArchiveDay, a.StorageKey, a.Bytes, a.Sha256, a.PublishedAt))
	}
	return out, nil
}

var _ RuntimeLogArchiveCatalog = (*PgStore)(nil)
var _ RuntimeLogArchiveCatalog = (*MemStore)(nil)
