package scheddgrpc

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/dbarchive"
)

func (s *Server) CheckDatabaseRestore(ctx context.Context, req *scheddpb.CheckDatabaseRestoreRequest) (*scheddpb.CheckDatabaseRestoreResponse, error) {
	d, err := s.databaseScheduler(ctx, callerAPID)
	if err != nil {
		return nil, err
	}
	point := req.GetPointInTime()
	if point == nil || !point.IsValid() || !dbarchive.ValidDatabaseID(req.GetDatabaseId()) {
		return nil, status.Error(codes.InvalidArgument, "a database and point in time are required")
	}
	var check dbarchive.RestoreCheck
	if req.GetRecoveryRegion() != "" {
		checker, ok := d.(interface {
			CheckRecoveryTarget(context.Context, string, string, time.Time, int, bool) (dbarchive.RestoreCheck, error)
		})
		if req.GetPostgresMajor() < 15 || req.GetPostgresMajor() > 18 {
			return nil, status.Error(codes.InvalidArgument, "unsupported PostgreSQL major")
		}
		if !ok {
			return nil, status.Error(codes.Unimplemented, "regional archive checks are unavailable")
		}
		check, err = checker.CheckRecoveryTarget(ctx, req.GetDatabaseId(), req.GetRecoveryRegion(), point.AsTime(), int(req.GetPostgresMajor()), req.GetLatest())
	} else {
		if req.GetLatest() {
			return nil, status.Error(codes.InvalidArgument, "latest requires a regional archive")
		}
		checker, ok := d.(interface {
			CheckRestoreTarget(context.Context, string, time.Time) (dbarchive.RestoreCheck, error)
		})
		if !ok {
			return nil, status.Error(codes.Unimplemented, "archive restore checks are unavailable")
		}
		check, err = checker.CheckRestoreTarget(ctx, req.GetDatabaseId(), point.AsTime())
	}
	if err != nil {
		return nil, s.databaseStatus("restore check", err)
	}
	out := &scheddpb.CheckDatabaseRestoreResponse{
		Outcome: check.Outcome, Reason: check.Reason, MissingWalSegment: check.MissingWAL,
		RecoveryTimeline: check.Timeline, MissingTimelineHistory: check.MissingHistory,
		RecoveryRegion: req.GetRecoveryRegion(),
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
