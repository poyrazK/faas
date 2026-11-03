package sched

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	DeploymentID   string
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
// A follow opened while the app is parked remains attached and discovers the
// instance created by a later wake. A node-level vmmd RPC failure is retried
// on the discovery cadence; surviving instances keep streaming. A one-shot
// request (follow=false) replays each currently live instance and returns as
// soon as every per-instance stream reaches EOF.
//
// Implementation notes:
//
//   - We open one goroutine per live instance (post-deployment-
//     filter). Each goroutine loops on vmmd.Logs.Recv and forwards
//     frames to the shared sink. The sink runs on the writer
//     goroutine so a slow consumer naturally backpressures all
//     per-instance streams.
//
//   - The vmmd Logs RPC emits io.EOF on a clean instance shutdown. The
//     goroutine exits and the discovery loop waits for a new live instance ID.
//     A non-EOF receive failure permits a bounded retry of the same ID.
//
//   - The writer goroutine owns the sink call (so the proto
//     marshal is serialised with the gRPC Send). The per-
//     instance readers send over a buffered channel; the writer
//     selects on sink err first to short-circuit on cancellation.
func (e *Engine) StreamAppLogs(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, follow bool, deploymentID string, sink LogFrameSink) error {
	if e.vmm == nil {
		return errors.New("sched: StreamAppLogs requires a vmm router")
	}
	if e.store == nil {
		return errors.New("sched: StreamAppLogs requires a store")
	}
	rows, err := e.liveInstancesForApp(ctx, appID)
	if err != nil {
		return fmt.Errorf("sched: StreamAppLogs list instances: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	frames := make(chan LogFrame, 32)
	type readerDone struct {
		instanceID string
		retry      bool
		err        error
	}
	done := make(chan readerDone, len(rows)+1)
	seen := make(map[string]bool)
	pending := 0
	// Per-instance resume state, touched only on this goroutine (attach and
	// the sink loops). Instances have independent seq spaces, and a vmmd
	// restart or a migration restarts an instance's seq at 1, so a re-attach
	// resumes by time, just after the last line delivered; one request-wide
	// since_seq used to lose a wake's first lines (0 = only new lines) or
	// duplicate a whole replay (>0) on every re-attach.
	nodeOf := make(map[string]string)
	lastAt := make(map[string]time.Time)
	firstAttachAt := make(map[string]time.Time)
	initial := make(map[string]bool, len(rows))
	for _, ins := range rows {
		initial[ins.ID] = true
	}
	deliver := func(f LogFrame) error {
		if !f.IsGap && f.InstanceID != "" && f.WrittenAt.After(lastAt[f.InstanceID]) {
			lastAt[f.InstanceID] = f.WrittenAt
		}
		return sink(f)
	}

	matches := func(ins state.Instance) bool {
		if !state.IsLive(ins.State) || ins.NodeID == "" {
			return false
		}
		return deploymentID == "" || ins.DeploymentID == "" || ins.DeploymentID == deploymentID
	}
	attach := func(ins state.Instance) {
		if !matches(ins) {
			return
		}
		if seen[ins.ID] && nodeOf[ins.ID] == ins.NodeID {
			return
		}
		// A migrated instance keeps its ID on a new node: the source ring
		// ended cleanly, so without the node check it never re-attached.
		resumed := seen[ins.ID] || nodeOf[ins.ID] != ""
		seen[ins.ID] = true
		nodeOf[ins.ID] = ins.NodeID
		if !follow {
			pending++
		}
		since, sinceAt := sinceSeq, sinceWrittenAt
		switch {
		case resumed:
			// A retry or a node change: everything after the last line
			// delivered, or after the first attach if none was.
			since, sinceAt = 1, firstAttachAt[ins.ID]
			if at, ok := lastAt[ins.ID]; ok {
				since, sinceAt = 1, at.Add(time.Nanosecond)
			}
		case !initial[ins.ID]:
			// Woke after the stream started: its whole ring is new.
			since = 1
		}
		if _, ok := firstAttachAt[ins.ID]; !ok {
			firstAttachAt[ins.ID] = time.Now()
		}
		go func() {
			stream, err := e.vmm.Logs(streamCtx, ins.NodeID, ins.ID, since, sinceAt, follow)
			if err != nil {
				select {
				case done <- readerDone{instanceID: ins.ID, retry: follow, err: err}:
				case <-streamCtx.Done():
				}
				return
			}
			retry := false
			var terminalErr error
			defer func() {
				select {
				case done <- readerDone{instanceID: ins.ID, retry: retry, err: terminalErr}:
				case <-streamCtx.Done():
				}
			}()
			for {
				line, err := stream.Recv()
				if err != nil {
					retry = follow && !errors.Is(err, io.EOF)
					if errors.Is(err, io.EOF) {
						terminalErr = nil
					} else if follow {
						terminalErr = nil
					} else {
						terminalErr = err
					}
					return
				}
				frame := LogFrame{
					InstanceID: ins.ID, DeploymentID: ins.DeploymentID,
					Seq: line.Seq, Stream: line.Stream, Line: line.Line,
					Level: line.Level, WrittenAt: line.WrittenAt,
					IsGap: line.IsGap, GapToWrittenAt: line.GapToWrittenAt, GapReason: line.GapReason,
				}
				if line.IsGap {
					frame.Seq, frame.Stream, frame.Line, frame.Level, frame.WrittenAt = 0, "", "", "", time.Time{}
				}
				select {
				case frames <- frame:
				case <-streamCtx.Done():
					return
				}
			}
		}()
	}
	for _, ins := range rows {
		attach(ins)
	}
	if !follow {
		if pending == 0 {
			return state.ErrNotFound
		}
		attached := pending
		var failed []error
		for pending > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case f := <-frames:
				if err := deliver(f); err != nil {
					return err
				}
			case ended := <-done:
				pending--
				if ended.err != nil {
					// One instance that cannot be read (still waking, or on
					// an unreachable node) is a gap in this response, not a
					// failure of the others' lines.
					failed = append(failed, ended.err)
					if err := deliver(LogFrame{InstanceID: ended.instanceID, IsGap: true, GapToWrittenAt: time.Now(), GapReason: "instance_unavailable"}); err != nil {
						return err
					}
				}
			}
		}
		if len(failed) == attached {
			return failed[0]
		}
		// Every reader queues its final done notification only after
		// queuing all preceding frames. Drain those buffered frames
		// before returning so a ready EOF cannot race the last log line.
		for {
			select {
			case f := <-frames:
				if err := deliver(f); err != nil {
					return err
				}
			default:
				return nil
			}
		}
	}

	// Instance IDs change whenever an app parks and wakes. Keep discovering
	// live rows for the lifetime of the follow stream instead of ending when
	// the rings that existed at attach time close.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case f := <-frames:
			if err := deliver(f); err != nil {
				return err
			}
		case ended := <-done:
			if ended.retry {
				// A dial can race vmmd's ring registration. Let the next poll
				// retry while the store still reports this instance as live;
				// attach resumes it after its last delivered line.
				delete(seen, ended.instanceID)
			}
		case <-ticker.C:
			current, listErr := e.liveInstancesForApp(streamCtx, appID)
			if listErr != nil {
				continue
			}
			for _, ins := range current {
				attach(ins)
			}
		}
	}
}
