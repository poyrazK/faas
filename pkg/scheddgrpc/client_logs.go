package scheddgrpc

import (
	"context"
	"errors"
	"io"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// LogFrame is the transport-neutral mirror of
// scheddpb.StreamAppLogsResponse (ADR-043 / Move 4 acceptance #5).
// Type-aliased to sched.LogFrame in pkg/scheddgrpc/server.go so
// there is exactly ONE struct definition to keep in sync when
// additive gap fields land — the wire encode/decode + the Recv
// adapter that surfaces this typed frame live here.
//
// instance_id is empty on the synthetic terminal frame (none emitted
// by Move 4 today; reserved for a future "end" marker).

// LogStream is the per-app log stream returned by Client.StreamAppLogs.
// Recv blocks until the next frame or the stream ends. A successful
// Recv returns (frame, nil) for a data frame; (zero, io.EOF) for a
// clean shutdown; (zero, gRPC-status) for any error.
//
// Errors pass through unchanged so callers can map gRPC codes
// (NotFound, Unavailable, Canceled) themselves. Do not call
// liftErr on these — the SSE renderer relies on the raw code to
// distinguish "no live instances" from "schedd unreachable".
type LogStream interface {
	Recv() (LogFrame, error)
}

// StreamAppLogs opens a server-streaming RPC against the schedd's
// StreamAppLogs endpoint (issue #254 / Move 4, issue #517 / PR-B
// acceptance #3 + #4). The schedd fans out per-instance vmmd Logs
// RPCs into one server stream; this method hands the caller a
// typed view of that stream.
//
// sinceSeq is the per-instance replay cursor forwarded verbatim
// to each vmmd (each instance's ring is independent; Seq scopes
// are per-instance, not global — see schedd.proto:268).
//
// sinceWrittenAt (issue #517 / PR-B acceptance #3) is the
// host-side lower bound on the per-instance written_at; the
// zero time is the "no bound" sentinel and is skipped on the
// wire.
//
// follow controls whether schedd remains subscribed after the
// retained replay page; false is the one-shot CLI mode.
//
// deploymentID (issue #517 / PR-B acceptance #3) is the
// per-deployment soft scoping; empty = fan out to every live
// instance for the app.
//
// level + grep (issue #309 / tier-2 DX) are the customer-facing
// --level / --grep filters. Both empty = no filter; the schedd
// applies them at the per-instance fan-out and increments
// apid_logs_dropped_total{reason="filter_level" or "filter_grep"}
// on drop. Empty values are skipped on the wire (proto3 omits
// the field when zero) so pre-issue-#309 schedd builds keep
// accepting the call unchanged (ADR-016 additive).
//
// Returned errors pass through unchanged. The caller owns error
// mapping; this method never lifts gRPC statuses to *api.Problem
// because the SSE renderer needs the raw code.
func (c *Client) StreamAppLogs(ctx context.Context, appID string, sinceSeq int64, sinceWrittenAt time.Time, follow bool, deploymentID string, level string, grep string) (LogStream, error) {
	req := &scheddpb.StreamAppLogsRequest{
		AppId:    appID,
		SinceSeq: sinceSeq,
		Follow:   &follow,
	}
	if !sinceWrittenAt.IsZero() {
		req.SinceWrittenAt = timestamppb.New(sinceWrittenAt)
	}
	if deploymentID != "" {
		req.DeploymentId = deploymentID
	}
	if level != "" {
		req.Level = level
	}
	if grep != "" {
		req.Grep = grep
	}
	stream, err := c.cli.StreamAppLogs(ctx, req)
	if err != nil {
		return nil, err
	}
	return &logStreamAdapter{inner: stream}, nil
}

// logStreamAdapter converts the proto stream into the typed
// LogStream surface. It does not transform errors — the raw gRPC
// status (or io.EOF) flows back to the caller so the SSE handler
// can map it via renderAppLogsError.
type logStreamAdapter struct {
	inner grpc.ServerStreamingClient[scheddpb.StreamAppLogsResponse]
}

var _ LogStream = (*logStreamAdapter)(nil)

// Recv blocks for the next frame or end-of-stream. WrittenAt is
// converted via timestamppb.AsTime; an unset proto timestamp
// yields the zero time.Time, which writeAppLogEvent renders as
// "0001-01-01T00:00:00Z" — Move 4's schedd always sets it, so
// callers can rely on the field being populated.
//
// On a gap frame (issue #517 / PR-B acceptance #4) the line-frame
// fields are zero and IsGap is true. GapToWrittenAt carries the
// ring's head-line WrittenAt and GapReason names the bound that
// triggered the gap, so the caller can render a meaningful
// diagnostic without guessing.
func (a *logStreamAdapter) Recv() (LogFrame, error) {
	resp, err := a.inner.Recv()
	if err != nil {
		// io.EOF and gRPC status errors pass through untouched.
		// The SSE handler distinguishes them via errors.Is(err,
		// io.EOF) and status.FromError respectively.
		if errors.Is(err, io.EOF) {
			return LogFrame{}, io.EOF
		}
		return LogFrame{}, err
	}
	frame := LogFrame{
		InstanceID: resp.GetInstanceId(),
		Seq:        resp.GetSeq(),
		Stream:     resp.GetStream(),
		Line:       resp.GetLine(),
		Level:      resp.GetLevel(),
		IsGap:      resp.GetIsGap(),
		GapReason:  resp.GetGapReason(),
	}
	if t := resp.GetWrittenAt(); t != nil {
		frame.WrittenAt = t.AsTime()
	}
	if t := resp.GetGapToWrittenAt(); t != nil {
		frame.GapToWrittenAt = t.AsTime()
	}
	return frame, nil
}
