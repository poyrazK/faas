package vmmdgrpc

import (
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Logs (issue #254 / Move 4) streams the per-instance ring buffer to the
// caller. Two phases:
//
//  1. Initial page: every line with Seq > sinceSeq is sent (Snapshot).
//  2. Live tail: Subscribe() returns a channel that delivers each new
//     committed line until the caller's context is cancelled or the
//     ring is closed (Kill/DestroyWithExport).
//
// A request with follow=false returns after phase 1. The optional wire field
// defaults to true so older schedd clients retain the live-tail behavior.
//
// PR-B (issue #517 / acceptance #3 + #4) extends the initial page
// with two additive filters without changing the wire contract for
// pre-PR-B clients:
//
//   - SinceWrittenAt: client-inclusive lower bound on the
//     host-side line.WrittenAt (RFC 3339). Empty = no time bound
//     (matches the pre-PR-B behaviour).
//   - Gap frame: when req.GetSinceSeq() falls below the ring's
//     lowest retained Seq, vmmd emits a synthetic `is_gap=true` frame
//     BEFORE the initial-page loop, carrying the head-written-at
//     timestamp so the client can surface a meaningful "you missed
//     lines whose newest retained time is X" message.
//
// The stream.Send call is the natural gRPC backpressure surface: a
// slow consumer starves the ring's Subscribe channel, which drops
// new lines via the ring's non-blocking publish (counter at apid).
//
// Wire status:
//
//   - codes.InvalidArgument when the caller forgot instance
//   - codes.NotFound when the instance is not alive on this vmmd
//   - codes.OK + nil on clean shutdown (caller cancels, ring closes)
//
// We do NOT return a server-streaming "end of stream" frame; gRPC
// carries the EOF out-of-band and the apid producer emits a terminal
// `event: end` frame on top of that.
//
// A since_seq cursor that fell below the ring's high-water mark is
// NOT an error — the producer emits the gap frame, then replays the
// lines it still has, then enters the live tail. Pre-PR-B clients
// that ignore the new frame see exactly the same line traffic they
// saw before (minus the evicted range, which they could not have
// replayed anyway).
func (s *Server) Logs(req *vmmdpb.LogsRequest, stream vmmdpb.Vmmd_LogsServer) error {
	const op = "Logs"
	start := time.Now()
	var sendErr error
	defer func() {
		s.ops.Observe(op, time.Since(start), sendErr)
	}()
	// issue #517: lift correlation fields off the inbound gRPC
	// metadata; the LogsRequest.wake_id proto field is the fallback
	// for callers that don't set the MD. PR-C will adopt canonical
	// timeline event names on these log lines.
	fields, _ := wire.CorrelationFromIncoming(stream.Context())
	if fields.WakeID == "" {
		fields.WakeID = req.GetWakeId()
	}
	streamLogger := wire.WithCorrelationFields(s.log, fields)
	if req.GetInstance() == "" {
		sendErr = status.Error(codes.InvalidArgument, "instance is required")
		streamLogger.Warn("vmmd: Logs: missing instance")
		return sendErr
	}
	ring := s.vmm.LogRing(req.GetInstance())
	if ring == nil {
		sendErr = status.Error(codes.NotFound, "instance not live on this vmmd")
		streamLogger.Info("vmmd: Logs: ring not found",
			"instance", req.GetInstance())
		return sendErr
	}
	var sinceTime time.Time
	if ts := req.GetSinceWrittenAt(); ts != nil {
		sinceTime = ts.AsTime()
	}
	streamLogger.Info("vmmd: Logs: stream opened",
		"instance", req.GetInstance(),
		"since_seq", req.GetSinceSeq(),
		"since_written_at", sinceTime)
	follow := true
	if req.Follow != nil {
		follow = req.GetFollow()
	}
	snapshotSince := req.GetSinceSeq()
	if !follow && snapshotSince == 0 {
		// The one-shot API has no cursor by default, so expose the
		// retained page rather than the live-tail sentinel used by
		// follow streams.
		snapshotSince = 1
	}
	// Gap-frame synthesis (issue #517 / PR-B acceptance #4). When
	// since_seq sits below the oldest line the ring currently
	// retains, the producer surfaces an explicit gap frame BEFORE
	// the initial-page loop. The frame is one-shot — subsequent
	// replies are line frames (mirror of the existing flow).
	//
	// The condition is gated on `since_seq > 0` because the "0 =
	// tail from now" sentinel must NOT trigger a gap frame on an
	// empty ring (the caller asked to skip the initial page). When
	// only the since_written_at bound could have triggered a gap
	// (i.e. the caller did not pass since_seq, or the ring's lowest
	// seq still covers it), we label the frame
	// "since_below_retained" so the consumer can render a meaningful
	// diagnostic instead of guessing.
	if req.GetSinceSeq() > 0 {
		lowest := ring.LowestRetainedSeq()
		if lowest > 0 && req.GetSinceSeq() < lowest {
			if err := stream.Send(gapResponse(ring.HeadWrittenAt(), "seq_below_retained")); err != nil {
				sendErr = err
				return err
			}
		}
	} else if !sinceTime.IsZero() {
		// since_seq omitted (live-tail sentinel) but a since-time
		// bound was passed AND the ring's oldest retained line is
		// strictly newer than the caller's bound: the caller asked
		// "give me everything since T" and the ring has nothing that
		// old. Surface an explicit gap frame labelled with the bound.
		headAt := ring.HeadWrittenAt()
		if !headAt.IsZero() && headAt.After(sinceTime) {
			if err := stream.Send(gapResponse(headAt, "since_below_retained")); err != nil {
				sendErr = err
				return err
			}
		}
	}
	// Initial page: lines with Seq > since_seq in commit order, plus
	// the since_written_at filter (issue #517 / PR-B acceptance #3).
	// SnapshotAndSubscribe registers the live subscriber under the same
	// lock as the snapshot so a line committed between these phases is
	// buffered for the live tail instead of being lost.
	snapshot, ch, cancel := ring.SnapshotAndSubscribe(snapshotSince)
	defer cancel()
	for _, line := range snapshot {
		if !sinceTime.IsZero() && line.WrittenAt.Before(sinceTime) {
			continue
		}
		if err := stream.Send(lineToPb(line)); err != nil {
			sendErr = err
			return err
		}
	}
	if !follow {
		return nil
	}
	// Live tail. SnapshotAndSubscribe returns an independent buffered
	// channel per subscriber; concurrent subscribers do not share
	// backpressure.
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case line, ok := <-ch:
			if !ok {
				// Ring closed — instance was Killed/Parked.
				return nil
			}
			if err := stream.Send(lineToPb(line)); err != nil {
				sendErr = err
				return err
			}
		}
	}
}

// lineToPb maps a ring Line to the proto envelope. The Seq/Stream/Line
// fields are 1:1; written_at is the host-side ingest time, never the
// guest clock (ADR-022 entropy hazard).
func lineToPb(l logbuf.Line) *vmmdpb.LogsResponse {
	return &vmmdpb.LogsResponse{
		Seq:       l.Seq,
		Stream:    l.Stream,
		Line:      l.Line,
		Level:     l.Level,
		WrittenAt: timestamppb.New(l.WrittenAt),
	}
}

// gapResponse builds the synthetic "cursor fell below the ring's
// high-water mark" frame (issue #517 / PR-B acceptance #4). The
// Seq/Stream/Line/WrittenAt line-frame fields are zero on the wire;
// is_gap is true, gap_to_written_at carries the head line's host-side
// ingest time so the client can render a meaningful diagnostic, and
// gap_reason names the bound that triggered the gap ("seq_below_retained"
// when the since_seq cursor is older than the ring's lowest retained
// seq, "since_below_retained" when the since_written_at cursor is
// older than the ring's oldest retained line). Wire shape mirrors
// the additive `is_gap` field PR-B introduced — pre-PR-B consumers
// ignore it.
func gapResponse(headAt time.Time, reason string) *vmmdpb.LogsResponse {
	resp := &vmmdpb.LogsResponse{IsGap: true, GapReason: reason}
	if !headAt.IsZero() {
		resp.GapToWrittenAt = timestamppb.New(headAt)
	}
	return resp
}
