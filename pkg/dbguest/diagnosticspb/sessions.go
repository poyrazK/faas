package diagnosticspb

import (
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func EncodeSessions(s *dbguest.SessionSample) *vmmdpb.DatabaseSessionSample {
	if s == nil {
		return nil
	}
	out := &vmmdpb.DatabaseSessionSample{SampledAt: timestamppb.New(s.SampledAt), HasMore: s.HasMore}
	for _, row := range s.Sessions {
		out.Sessions = append(out.Sessions, &vmmdpb.DatabaseSessionSample_Session{
			Pid: row.PID, State: row.State, TransactionAgeMs: row.TransactionAgeMS, WaitEventType: row.WaitEventType,
			WaitEvent: row.WaitEvent, ApplicationName: row.ApplicationName, TargetId: row.TargetID,
		})
	}
	return out
}

func DecodeSessions(s *vmmdpb.DatabaseSessionSample) *dbguest.SessionSample {
	if s.GetSampledAt().CheckValid() != nil || len(s.GetSessions()) > dbguest.MaxSessionRows {
		return nil
	}
	out := &dbguest.SessionSample{
		SampledAt: s.GetSampledAt().AsTime(), HasMore: s.GetHasMore(), Sessions: make([]dbguest.SessionRow, 0, len(s.GetSessions())),
	}
	for _, row := range s.GetSessions() {
		if row.GetPid() <= 0 || row.GetTransactionAgeMs() < 0 {
			return nil
		}
		out.Sessions = append(out.Sessions, dbguest.SessionRow{
			PID: row.GetPid(), State: row.GetState(), TransactionAgeMS: row.GetTransactionAgeMs(), WaitEventType: row.GetWaitEventType(),
			WaitEvent: row.GetWaitEvent(), ApplicationName: row.GetApplicationName(), TargetID: row.GetTargetId(),
		})
	}
	return out
}
