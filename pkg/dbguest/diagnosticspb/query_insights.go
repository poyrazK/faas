package diagnosticspb

import (
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbqueryinsights"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func EncodeQuerySample(s *dbqueryinsights.Sample) *vmmdpb.DatabaseQuerySample {
	if s == nil || !s.Valid() {
		return nil
	}
	out := &vmmdpb.DatabaseQuerySample{At: timestamppb.New(s.At), BootAt: timestamppb.New(s.BootAt),
		Evictions: s.Evictions}
	if !s.ResetAt.IsZero() {
		out.ResetAt = timestamppb.New(s.ResetAt)
	}
	for _, c := range s.Counters {
		out.Counters = append(out.Counters, &vmmdpb.DatabaseQuerySample_Counter{
			QueryId: c.QueryID, UserId: c.UserID, Calls: c.Calls, ExecutionMs: c.ExecutionMS,
		})
	}
	return out
}

func DecodeQuerySample(s *vmmdpb.DatabaseQuerySample) *dbqueryinsights.Sample {
	if s == nil || s.GetAt().CheckValid() != nil || s.GetBootAt().CheckValid() != nil ||
		len(s.GetCounters()) > dbqueryinsights.MaxEntries {
		return nil
	}
	out := &dbqueryinsights.Sample{At: s.GetAt().AsTime(), BootAt: s.GetBootAt().AsTime(), Evictions: s.GetEvictions()}
	if reset := s.GetResetAt(); reset != nil {
		if reset.CheckValid() != nil {
			return nil
		}
		out.ResetAt = reset.AsTime()
	}
	for _, c := range s.GetCounters() {
		out.Counters = append(out.Counters, dbqueryinsights.Counter{
			QueryID: c.GetQueryId(), UserID: c.GetUserId(), Calls: c.GetCalls(), ExecutionMS: c.GetExecutionMs(),
		})
	}
	if !out.Valid() {
		return nil
	}
	return out
}
