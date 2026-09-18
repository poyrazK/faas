package vmmdgrpc

import (
	"context"
	"sort"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/sched/flowcount"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const flowSummaryEventName = "gregale.flow.summary"

func flowSummariesToProto(rows []flowcount.FlowSummary) []*vmmdpb.FlowSummary {
	if len(rows) == 0 {
		return nil
	}
	out := make([]*vmmdpb.FlowSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, &vmmdpb.FlowSummary{
			Protocol:   row.Protocol,
			RemoteIp:   row.RemoteIP,
			RemotePort: uint32(row.RemotePort),
			State:      row.State,
			Direction:  row.Direction,
			Count:      row.Count,
		})
	}
	return out
}

// emitFlowSummarySpan records the bounded flow snapshot as OTel events on a
// dedicated child span. Events carry only endpoint-level metadata; no packet
// payload, URL, header, or byte contents enter the trace.
func emitFlowSummarySpan(ctx context.Context, summaries map[string][]flowcount.FlowSummary) {
	if len(summaries) == 0 {
		return
	}
	_, span := pkgtrace.StartSpan(ctx, "vmmd.flow.snapshot",
		attribute.Int("gregale.flow.instance_count", len(summaries)))
	defer span.End()

	instanceIDs := make([]string, 0, len(summaries))
	for instanceID := range summaries {
		instanceIDs = append(instanceIDs, instanceID)
	}
	sort.Strings(instanceIDs)
	for _, instanceID := range instanceIDs {
		for _, row := range summaries[instanceID] {
			span.AddEvent(flowSummaryEventName, oteltrace.WithAttributes(
				attribute.String("gregale.flow.instance_id", instanceID),
				attribute.String("gregale.flow.protocol", row.Protocol),
				attribute.String("gregale.flow.remote_ip", row.RemoteIP),
				attribute.Int("gregale.flow.remote_port", int(row.RemotePort)),
				attribute.String("gregale.flow.state", row.State),
				attribute.String("gregale.flow.direction", row.Direction),
				attribute.Int64("gregale.flow.count", row.Count),
			))
		}
	}
}
