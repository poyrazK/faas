package firecracker

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (p *Provider) CheckRestoreTarget(ctx context.Context, id string, point time.Time) (api.ManagedPostgresRestoreCheck, error) {
	unknown := api.ManagedPostgresRestoreCheck{Outcome: "unverified", Reason: "archive_unavailable"}
	scheduler, ok := p.scheduler.(interface {
		CheckDatabaseRestore(context.Context, *scheddpb.CheckDatabaseRestoreRequest, ...grpc.CallOption) (*scheddpb.CheckDatabaseRestoreResponse, error)
	})
	if !ok {
		return unknown, nil
	}
	response, err := scheduler.CheckDatabaseRestore(ctx, &scheddpb.CheckDatabaseRestoreRequest{
		DatabaseId: resourceUUID(id), PointInTime: timestamppb.New(point),
	})
	switch status.Code(err) {
	case codes.Unimplemented, codes.Unavailable, codes.DeadlineExceeded:
		return unknown, nil
	case codes.OK:
	default:
		return api.ManagedPostgresRestoreCheck{}, providerError(err)
	}
	result := api.ManagedPostgresRestoreCheck{
		Outcome: response.GetOutcome(), Reason: response.GetReason(), MissingWALSegment: response.GetMissingWalSegment(),
		RecoveryTimeline: response.GetRecoveryTimeline(), MissingTimelineHistory: response.GetMissingTimelineHistory(),
	}
	if response.GetBackupCompletedAt() != nil {
		at := response.GetBackupCompletedAt().AsTime()
		result.BackupCompletedAt = &at
	}
	if response.GetWalStopAt() != nil {
		at := response.GetWalStopAt().AsTime()
		result.WALStopAt = &at
	}
	return result, nil
}

func (p *Provider) CheckRecoveryTarget(ctx context.Context, id string, major int, point time.Time, latest bool) (api.ManagedPostgresRecoveryStatus, error) {
	unknown := api.ManagedPostgresRecoveryStatus{Region: p.cfg.Region, Outcome: "unverified", Reason: "recovery_archive_unavailable"}
	scheduler, ok := p.scheduler.(interface {
		CheckDatabaseRestore(context.Context, *scheddpb.CheckDatabaseRestoreRequest, ...grpc.CallOption) (*scheddpb.CheckDatabaseRestoreResponse, error)
	})
	if !ok {
		return unknown, nil
	}
	reply, err := scheduler.CheckDatabaseRestore(ctx, &scheddpb.CheckDatabaseRestoreRequest{
		DatabaseId: resourceUUID(id), PointInTime: timestamppb.New(point), RecoveryRegion: p.cfg.Region,
		PostgresMajor: int32(major), Latest: latest,
	})
	switch status.Code(err) {
	case codes.Unimplemented, codes.Unavailable, codes.DeadlineExceeded:
		return unknown, nil
	case codes.OK:
	default:
		return unknown, providerError(err)
	}
	if reply.GetRecoveryRegion() != p.cfg.Region {
		return unknown, nil
	}
	result := api.ManagedPostgresRecoveryStatus{
		Region: p.cfg.Region, Outcome: reply.GetOutcome(), Reason: reply.GetReason(), MissingWALSegment: reply.GetMissingWalSegment(),
		RecoveryTimeline: reply.GetRecoveryTimeline(), MissingTimelineHistory: reply.GetMissingTimelineHistory(),
	}
	verified := reply.GetRecoveryPoint()
	if latest && result.Outcome == "covered" {
		if !verified.IsValid() || verified.AsTime().After(point) {
			return unknown, managedpostgres.ErrUnavailable
		}
		at := verified.AsTime()
		result.RecoveryPoint = &at
	}
	return result, nil
}
