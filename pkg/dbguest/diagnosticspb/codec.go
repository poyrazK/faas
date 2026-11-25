package diagnosticspb

import (
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func Encode(s *dbguest.ActivitySample) *vmmdpb.DatabaseActivitySample {
	if s == nil {
		return nil
	}
	out := &vmmdpb.DatabaseActivitySample{
		SampledAt: timestamppb.New(s.SampledAt), Connections: s.Connections, ConnectionLimit: s.ConnectionLimit,
		ActiveTransactions: s.ActiveTransactions, OldestActiveTransactionMs: s.OldestActiveTransactionMS,
		IdleInTransaction: s.IdleInTransaction, OldestIdleTransactionMs: s.OldestIdleTransactionMS, LockWaits: s.LockWaits,
	}
	for _, wait := range s.BlockedSessions {
		out.BlockedSessions = append(out.BlockedSessions, &vmmdpb.DatabaseActivitySample_BlockedSession{
			Pid: wait.PID, BlockingPids: wait.BlockingPIDs,
		})
	}
	return out
}

func Decode(s *vmmdpb.DatabaseActivitySample) *dbguest.ActivitySample {
	if s == nil || s.GetSampledAt().CheckValid() != nil || s.GetConnectionLimit() <= 0 {
		return nil
	}
	out := &dbguest.ActivitySample{
		SampledAt: s.GetSampledAt().AsTime(), Connections: s.GetConnections(), ConnectionLimit: s.GetConnectionLimit(),
		ActiveTransactions: s.GetActiveTransactions(), OldestActiveTransactionMS: s.GetOldestActiveTransactionMs(),
		IdleInTransaction: s.GetIdleInTransaction(), OldestIdleTransactionMS: s.GetOldestIdleTransactionMs(), LockWaits: s.GetLockWaits(),
		BlockedSessions: make([]dbguest.LockWait, 0, len(s.GetBlockedSessions())),
	}
	for _, wait := range s.GetBlockedSessions() {
		pids := append([]int32{}, wait.GetBlockingPids()...)
		out.BlockedSessions = append(out.BlockedSessions, dbguest.LockWait{PID: wait.GetPid(), BlockingPIDs: pids})
	}
	return out
}
