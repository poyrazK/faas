package diagnosticspb

import (
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func EncodeTableHealthOptions(o *dbguest.TableHealthOptions) *vmmdpb.DatabaseTableHealthOptions {
	if o == nil {
		return nil
	}
	return &vmmdpb.DatabaseTableHealthOptions{Sort: o.Sort, Limit: int32(o.Limit)}
}

func DecodeTableHealthOptions(o *vmmdpb.DatabaseTableHealthOptions) *dbguest.TableHealthOptions {
	if o == nil {
		return nil
	}
	return &dbguest.TableHealthOptions{Sort: o.GetSort(), Limit: int(o.GetLimit())}
}

func EncodeTableHealth(s *dbguest.TableHealthSample) *vmmdpb.DatabaseTableHealthSample {
	if s == nil {
		return nil
	}
	out := &vmmdpb.DatabaseTableHealthSample{SampledAt: timestamppb.New(s.SampledAt), HasMore: s.HasMore}
	for _, table := range s.Tables {
		row := &vmmdpb.DatabaseTableHealthSample_Table{
			Schema: table.Schema, Name: table.Name, TableBytes: table.TableBytes, IndexBytes: table.IndexBytes,
			EstimatedDeadRows: table.EstimatedDeadRows, ChangesSinceAnalyze: table.ChangesSinceAnalyze, XidAge: table.XIDAge,
		}
		if table.LastVacuumAt != nil {
			row.LastVacuumAt = timestamppb.New(*table.LastVacuumAt)
		}
		if table.LastAnalyzeAt != nil {
			row.LastAnalyzeAt = timestamppb.New(*table.LastAnalyzeAt)
		}
		out.Tables = append(out.Tables, row)
	}
	return out
}

func DecodeTableHealth(s *vmmdpb.DatabaseTableHealthSample) *dbguest.TableHealthSample {
	if s == nil || s.GetSampledAt().CheckValid() != nil || len(s.GetTables()) > dbguest.TableHealthMaxLimit {
		return nil
	}
	out := &dbguest.TableHealthSample{
		SampledAt: s.GetSampledAt().AsTime(), HasMore: s.GetHasMore(),
		Tables: make([]dbguest.TableHealthRow, 0, len(s.GetTables())),
	}
	for _, row := range s.GetTables() {
		out.Tables = append(out.Tables, dbguest.TableHealthRow{
			Schema: row.GetSchema(), Name: row.GetName(), TableBytes: row.GetTableBytes(), IndexBytes: row.GetIndexBytes(),
			EstimatedDeadRows: row.GetEstimatedDeadRows(), ChangesSinceAnalyze: row.GetChangesSinceAnalyze(), XIDAge: row.GetXidAge(),
			LastVacuumAt: optionalTime(row.GetLastVacuumAt()), LastAnalyzeAt: optionalTime(row.GetLastAnalyzeAt()),
		})
	}
	return out
}

func optionalTime(t *timestamppb.Timestamp) *time.Time {
	if t.CheckValid() != nil {
		return nil
	}
	at := t.AsTime()
	return &at
}
