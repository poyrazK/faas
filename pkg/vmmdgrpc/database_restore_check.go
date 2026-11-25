package vmmdgrpc

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbarchive"
)

func (s *Server) DatabaseArchiveRestoreCheck(ctx context.Context, req *vmmdpb.DatabaseArchiveRestoreCheckRequest) (*vmmdpb.DatabaseArchiveRestoreCheckResponse, error) {
	rt, err := s.databaseRuntime(ctx, databaseCallerSchedd)
	if err != nil {
		return nil, err
	}
	point := req.GetPointInTime()
	if point == nil || !point.IsValid() || !dbarchive.ValidDatabaseID(req.GetDatabaseId()) ||
		req.GetPostgresMajor() < 12 || req.GetPostgresMajor() > 99 {
		return nil, status.Error(codes.InvalidArgument, "a database, point in time, and PostgreSQL major are required")
	}
	var check dbarchive.RestoreCheck
	if req.GetFromRecoveryArchive() {
		checker, ok := rt.(interface {
			CheckRecoveryArchive(context.Context, string, time.Time, int, bool) (dbarchive.RestoreCheck, error)
		})
		if !ok {
			return nil, status.Error(codes.Unimplemented, "regional archive checks are unavailable")
		}
		check, err = checker.CheckRecoveryArchive(ctx, req.GetDatabaseId(), point.AsTime(), int(req.GetPostgresMajor()), req.GetLatest())
	} else if req.GetLatest() {
		checker, ok := rt.(interface {
			CheckLatestArchiveRestore(context.Context, string, time.Time, int) (dbarchive.RestoreCheck, error)
		})
		if !ok {
			return nil, status.Error(codes.Unimplemented, "latest archive checks are unavailable")
		}
		check, err = checker.CheckLatestArchiveRestore(ctx, req.GetDatabaseId(), point.AsTime(), int(req.GetPostgresMajor()))
	} else {
		checker, ok := rt.(interface {
			CheckArchiveRestore(context.Context, string, time.Time, int) (dbarchive.RestoreCheck, error)
		})
		if !ok {
			return nil, status.Error(codes.Unimplemented, "archive restore checks are unavailable")
		}
		check, err = checker.CheckArchiveRestore(ctx, req.GetDatabaseId(), point.AsTime(), int(req.GetPostgresMajor()))
	}
	if err != nil {
		return nil, s.databaseStatus("archive restore check", err)
	}
	out := &vmmdpb.DatabaseArchiveRestoreCheckResponse{
		Outcome: check.Outcome, Reason: check.Reason, MissingWalSegment: check.MissingWAL,
		RecoveryTimeline: check.Timeline, MissingTimelineHistory: check.MissingHistory,
		FromRecoveryArchive: req.GetFromRecoveryArchive(),
	}
	if check.BackupCompleted != nil {
		out.BackupCompletedAt = timestamppb.New(*check.BackupCompleted)
	}
	if check.WALStopTime != nil {
		out.WalStopAt = timestamppb.New(*check.WALStopTime)
	}
	if check.RecoveryPoint != nil {
		out.RecoveryPoint = timestamppb.New(*check.RecoveryPoint)
	}
	return out, nil
}
