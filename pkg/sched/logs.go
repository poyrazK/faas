package sched

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// LogFrameSink is the per-frame callback the StreamAppLogs handler
// invokes for each frame decoded from the per-instance vmmd Logs
// RPC. It returns a non-nil error to abort the stream (the gRPC
// trailer carries it back to the apid caller); a nil return tells
// the handler to keep delivering the next frame.
//
// The production caller (pkg/scheddgrpc.Server.StreamAppLogs) renders
// the frame to a scheddpb.StreamAppLogsResponse proto and forwards
// it on the caller's gRPC stream. That work is bounded by the
// per-frame size (≤ a few KB); long-running work inside the
// callback would stall backpressure on the matching vmmd Logs
// stream.
//
// The writer goroutine that owns the gRPC stream only ever calls
// this from the select-arm that owns the Send — so the callback
// is serialised with the gRPC write and cannot race with the
// reader-side Recv.
//
// Shape (PR-B / issue #517 acceptance #4): the frame is a LogFrame
// struct so the same callback handles both line frames and gap
// frames (the synthetic gap frame sets IsGap=true and carries the
// ring's current head timestamp in GapToWrittenAt). Pre-PR-B
// callers that ignore the additive fields see only line frames.
type LogFrameSink func(LogFrame) error

// LogFrame is the typed in-process shape that crosses the
// per-instance vmmd Logs boundary into the schedd-side fan-out. It
// is structurally identical to scheddgrpc.LogFrame (the wire-
// neutral mirror that crosses the schedd gRPC hop) — the two are
// coupled by a type alias in pkg/scheddgrpc/server.go so the
// schedd gRPC handler can hand a sched.LogFrame straight to its
// own sink.
//
// IsGap is the additive issue #517 / PR-B acceptance #4 flag
// (true on the synthetic "cursor fell below the ring's high-water
// mark" frame). GapToWrittenAt is meaningful only when IsGap is
// true; it carries the host-side ingest time of the OLDEST line
// the ring currently retains so the client can render a
// meaningful "you missed lines whose newest retained time is X"
// message. Pre-PR-B sinks ignore both fields.
//
// InstanceID + Seq + Stream + Line + WrittenAt follow the Move 4
// contract verbatim; the line-frame fields are unset on a gap
// frame (the wire shape mirrors the vmmd response: Seq=0,
// Stream="", Line=""). GapReason names the bound that triggered
// the gap; meaningful only when IsGap is true.
type LogFrame struct {
	InstanceID     string
	Seq            int64
	Stream         string
	Line           string
	Level          string
	WrittenAt      time.Time
	IsGap          bool
	GapToWrittenAt time.Time
	GapReason      string
}

// StreamAppLogs (issue #254 / Move 4, issue #517 / PR-B) is the
// schedd-side fan-out for the per-app log stream. The engine
// resolves the live instances via the store, opens one vmmd Logs
// RPC per instance via the RoutedVMM, and invokes the sink for
// every frame (initial-page + live tail) until the context
// cancels.
//
// PR-B extends the signature with two new optional filter args:
//
//   - sinceWrittenAt (issue #517 / PR-B acceptance #3): the
//     client-inclusive lower bound on the host-side ring
//     WrittenAt. Empty = no time bound. Forwarded verbatim to
//     each vmmd per-instance Logs RPC.
//   - deploymentID (issue #517 / PR-B acceptance #3): the
//     per-deployment soft scoping. Empty = fan out to every
//     live instance for the app; non-empty = skip instances
//     whose Instance.DeploymentID does not match. Instances
//     with an empty DeploymentID match any non-empty
//     deploymentID filter (legacy rows are unmatched-only when
//     the filter itself is empty).
//
// Returns state.ErrNotFound when the app has zero live instances
// after the deployment filter is applied (apid maps this to its
// 404 "the app is parked; wake it first"). A node-level vmmd RPC
// failure logs and continues — the surviving instances keep
// streaming.
//
// Implementation notes:
//
//   - We open one goroutine per live instance (post-deployment-
//     filter). Each goroutine loops on vmmd.Logs.Recv and forwards
//     frames to the shared sink. The sink runs on the writer
//     goroutine so a slow consumer naturally backpressures all
//     per-instance streams.
//
//   - The vmmd Logs RPC emits io.EOF on a clean instance shutdown
//     (ring closed → subscribe channel drained). The goroutine
//     exits on EOF and the wart set shrinks. The outer loop
//     blocks on wg.Wait() until all instances have ended or
//     the context cancels.
//
//   - The writer goroutine owns the sink call (so the proto
//     marshal is serialised with the gRPC Send). The per-
//     instance readers send over a buffered channel; the writer
//     selects on sink err first to short-circuit on cancellation.
func (e *Engine) StreamAppLogs(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, deploymentID string, sink LogFrameSink) error {
	if e.vmm == nil {
		return errors.New("sched: StreamAppLogs requires a vmm router")
	}
	if e.store == nil {
		return errors.New("sched: StreamAppLogs requires a store")
	}
	rows, err := e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return fmt.Errorf("sched: StreamAppLogs list instances: %w", err)
	}
	live := make([]state.Instance, 0, len(rows))
	for _, ins := range rows {
		if !state.IsLive(ins.State) || ins.NodeID == "" {
			continue
		}
		// PR-B deployment filter: skip instances whose
		// DeploymentID is set and disagrees with the filter.
		// An instance with an empty DeploymentID matches any
		// non-empty filter — forward-compat with legacy rows
		// (the column was added in M7 / PR #169 but adoption
		// is gradual).
		if deploymentID != "" && ins.DeploymentID != "" && ins.DeploymentID != deploymentID {
			continue
		}
		live = append(live, ins)
	}
	if len(live) == 0 {
		return state.ErrNotFound
	}
	// Adapter: each per-instance goroutine sends LogFrame tuples
	// on this channel; the writer goroutine reads them and
	// invokes the sink. Keeping the proto marshal on the writer
	// goroutine lets us serialise the gRPC Send with the callback
	// chain.
	ch := make(chan LogFrame, 32)
	var wg sync.WaitGroup
	for _, ins := range live {
		ins := ins
		wg.Add(1)
		go func() {
			defer wg.Done()
			stream, err := e.vmm.Logs(ctx, ins.NodeID, ins.ID, sinceSeq, sinceWrittenAt)
			if err != nil {
				// Per-instance dial failure is non-fatal;
				// the surviving instances keep streaming.
				return
			}
			for {
				line, err := stream.Recv()
				if err != nil {
					// io.EOF (clean shutdown) or a
					// gRPC status error. Either way,
					// this instance is done.
					return
				}
				frame := LogFrame{
					InstanceID:     ins.ID,
					Seq:            line.Seq,
					Stream:         line.Stream,
					Line:           line.Line,
					Level:          line.Level,
					WrittenAt:      line.WrittenAt,
					IsGap:          line.IsGap,
					GapToWrittenAt: line.GapToWrittenAt,
					GapReason:      line.GapReason,
				}
				// Gap frames must NOT carry stale line-frame
				// values from any prior frame that reused the
				// same buffer (vmmdgrpc zero-initialises but we
				// belt-and-brace here so a future transport
				// change can't accidentally leak seq/line into
				// a gap event).
				if line.IsGap {
					frame.Seq = 0
					frame.Stream = ""
					frame.Line = ""
					frame.Level = ""
					frame.WrittenAt = time.Time{}
				}
				select {
				case <-ctx.Done():
					return
				case ch <- frame:
				}
			}
		}()
	}
	// Closer: closes the frame channel once all instance goroutines
	// have ended, so the writer's range loop exits cleanly.
	go func() {
		wg.Wait()
		close(ch)
	}()
	for f := range ch {
		if err := sink(f); err != nil {
			// Bubble up so pkg/scheddgrpc.Server.StreamAppLogs can
			// carry the gRPC trailer (the sink error is the
			// per-frame Send failure; a context.Canceled or
			// codes.Unavailable-from-StreamAppLogs). The
			// per-instance reader goroutines exit on ctx.Done()
			// inside their select; the closer goroutine drains
			// them via wg.Wait.
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}
