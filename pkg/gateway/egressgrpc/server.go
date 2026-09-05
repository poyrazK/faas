// Package egressgrpc is the gateway-side implementation of the
// ADR-046 PR-2 producer channel
// (onebox.faas.egress.v1.EgressTxService.StreamBytes). The
// service drains the per-instance ring buffer
// (pkg/gateway/egresssink.EgressSink) on a fixed cadence and
// pushes one BytesFrame per (instance_id, minute) bucket through
// the server-streaming RPC.
//
// Layering (gateway side):
//
//	cmd/gatewayd-internal/main.go registers an EgressTxServiceServer
//	alongside the existing SynthServer on a single *grpc.Server
//	bound to the same /run/faas/gatewayd-internal.sock unix-domain
//	socket (FAAS_GATEWAY_SYNTH_SOCKET). The unix-socket DAC auth
//	(ADR-015) is the only authentication for v1 — only meterd is
//	in the `faas` group, so the socket IS the auth.
//
// Why server-streaming rather than unary:
//
//	Drain cadence is governed by the meterd sample tick (1/min in
//	cmd/meterd/main.go). The producer side (Handler.recordEgress)
//	fires per HTTP response, ~µs apart. A unary request/response
//	would either spam the dialer (every flushed proxy response ⇒
//	one RTT) or batch-and-delay (lose attribution granularity).
//	Server-streaming inverts the cost: one persistent connection,
//	the server accumulates, the client reads at its own pace.
//
// Why a fixed cadence rather than push-on-record:
//
//	Inbound HTTP traffic can spike (the §13 load test fires
//	thousands of req/s at the gateway). Pushing one stream frame
//	per HTTP response back-to-back would saturate the unix socket
//	and let the meterd dialer fall behind. A 1 Hz cadence (the
//	same as schedd's per-instance stats poll) bounds the
//	frame-rate at 1/s while keeping worst-case latency at ~1 s,
//	well inside the §4.7 billing-window slack.
package egressgrpc

import (
	"log/slog"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	egresspb "github.com/onebox-faas/faas/api/proto/onebox/faas/egress/v1"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
)

// StreamCadence is how often the server-side drain ticks. See
// the package doc for the rationale; this variable is exported
// so tests can shrink the period without time.Sleep races.
var StreamCadence = 1 * time.Second

// Server is the gateway-side EgressTxService implementation. One
// per gatewayd-internal process; constructed in cmd/gatewayd-internal/main.go
// after the Handler + EgressSink are wired.
//
// Concurrency: the embedded *grpc.Server handles per-stream
// goroutines; drains are issued from the cadence ticker on each
// per-stream goroutine, so multiple streams run in parallel.
// The egressSink is the single shared producer/consumer point
// and is independently race-safe
// (pkg/gateway/egresssink package doc).
type Server struct {
	egresspb.UnimplementedEgressTxServiceServer

	sink *egresssink.EgressSink
	log  *slog.Logger

	framesSent    atomic.Uint64 // cumulative frames across every active stream
	activeStreams atomic.Int32  // current open stream count (admin/debug seam)
}

// NewServer wires the bare service. log defaults to
// slog.Default() if nil. sink is required for the service to be
// useful; the StreamBytes handler returns immediately (no
// frames emitted) if sink is nil and the connection stays open
// so the client doesn't see spurious EOFs.
func NewServer(sink *egresssink.EgressSink, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{sink: sink, log: log}
}

// FramesSent is the cumulative count of BytesFrame messages
// emitted across every active stream — the operator-side metric
// for "the producer channel is alive". Surfaced via the control
// listener's /debug (or an internal admin endpoint); the
// canonical "bytes egressed" view is the per-(app, plan) counter
// on /metrics.
func (s *Server) FramesSent() uint64 { return s.framesSent.Load() }

// ActiveStreams is the number of currently-open streams.
// Admin/debug seam; nil-safe on the receiver because atomic.Int32
// is value-typed.
func (s *Server) ActiveStreams() int { return int(s.activeStreams.Load()) }

// StreamBytes is the single RPC. The per-stream goroutine owns
// the cadence ticker and drains the sink on every tick,
// forwarding one frame per (instance_id, minute) bucket. The
// stream stays open until ctx cancels (meterd hangs up,gatewayd-internal
// shuts down) — meterd's dialer reconnects on transient failures
// (P0 gatewayd-internal restart), picking up from the next cadence tick.
//
// Empty drains are silent (zero-frame emits) so a working
// gatewayd-internal that happens to have no observed bytes this tick
// doesn't trigger spurious "no data" alerts on the meterd side.
func (s *Server) StreamBytes(req *egresspb.StreamBytesRequest, stream grpc.ServerStreamingServer[egresspb.BytesFrame]) error {
	if s.sink == nil {
		s.log.Warn("egressgrpc: stream opened but sink is nil; refusing with empty stream")
		return nil
	}
	ctx := stream.Context()
	s.activeStreams.Add(1)
	defer s.activeStreams.Add(-1)

	s.log.Debug("egressgrpc: stream opened")
	t := time.NewTicker(StreamCadence)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			s.log.Debug("egressgrpc: stream closed by client", "frames", s.framesSent.Load())
			return nil
		case <-t.C:
			for _, rec := range s.sink.DrainRecords() {
				if err := stream.Send(&egresspb.BytesFrame{
					InstanceId: rec.InstanceID,
					Minute:     timestamppb.New(rec.Minute.UTC()),
					Bytes:      rec.Bytes,
					Requests:   rec.Requests,
					ColdBoots:  rec.ColdBoots,
				}); err != nil {
					s.log.Warn("egressgrpc: send failed; closing stream", "err", err)
					return nil
				}
				s.framesSent.Add(1)
			}
		}
	}
}
