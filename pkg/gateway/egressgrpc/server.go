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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	egresspb "github.com/onebox-faas/faas/api/proto/onebox/faas/egress/v1"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
)

// StreamCadence is how often the server-side drain ticks. See
// the package doc for the rationale; this variable is exported
// so tests can shrink the period without time.Sleep races.
var StreamCadence = 1 * time.Second

// DefaultPendingPath is the compute-local durable replay ledger. It lives on
// the host boot disk, outside the Firecracker snapshot volume, so fsyncs do not
// contend with the customer restore path.
const DefaultPendingPath = "/var/lib/faas/egress-meter/pending.json"

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
	pendingMu     sync.Mutex
	pending       map[string]egresssink.Record
	pendingPath   string
	pendingDirty  bool
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
	return &Server{sink: sink, log: log, pending: make(map[string]egresssink.Record)}
}

// NewPersistentServer restores the unacknowledged replay set from disk. A
// corrupt ledger fails startup: silently replacing it would turn a local file
// problem into unreported customer-usage loss.
func NewPersistentServer(sink *egresssink.EgressSink, log *slog.Logger, pendingPath string) (*Server, error) {
	s := NewServer(sink, log)
	s.pendingPath = pendingPath
	if pendingPath == "" {
		return s, nil
	}
	if err := s.loadPending(); err != nil {
		return nil, err
	}
	return s, nil
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
			records, err := s.replayRecords()
			if err != nil {
				s.log.Warn("egressgrpc: durable replay ledger write failed", "err", err)
				continue
			}
			if err := s.sendRecords(records, stream.Send); err != nil {
				s.log.Warn("egressgrpc: send failed; closing stream", "err", err)
				return nil
			}
		}
	}
}

type replayRecord struct {
	eventID string
	record  egresssink.Record
}

func (s *Server) replayRecords() ([]replayRecord, error) {
	newRecords := s.sink.DrainRecords()
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for _, record := range newRecords {
		s.pending[uuid.NewString()] = record
	}
	if len(newRecords) > 0 {
		s.pendingDirty = true
	}
	if s.pendingDirty {
		if err := s.persistPendingLocked(s.pending); err != nil {
			return nil, err
		}
		s.pendingDirty = false
	}
	ids := make([]string, 0, len(s.pending))
	for eventID := range s.pending {
		ids = append(ids, eventID)
	}
	sort.Strings(ids)
	out := make([]replayRecord, 0, len(ids))
	for _, eventID := range ids {
		out = append(out, replayRecord{eventID: eventID, record: s.pending[eventID]})
	}
	return out, nil
}

func (s *Server) sendRecords(records []replayRecord, send func(*egresspb.BytesFrame) error) error {
	for _, pending := range records {
		rec := pending.record
		if err := send(&egresspb.BytesFrame{
			InstanceId: rec.InstanceID,
			Minute:     timestamppb.New(rec.Minute.UTC()),
			Bytes:      rec.Bytes,
			Requests:   rec.Requests,
			ColdBoots:  rec.ColdBoots,
			EventId:    pending.eventID,
		}); err != nil {
			// Every frame remains in pending until meterd confirms its
			// Postgres idempotency row. A reconnect therefore replays both
			// the failed frame and earlier sent-but-unacknowledged frames.
			return err
		}
		s.framesSent.Add(1)
	}
	return nil
}

func (s *Server) AckBytes(_ context.Context, req *egresspb.AckBytesRequest) (*egresspb.AckBytesResponse, error) {
	if req == nil {
		return &egresspb.AckBytesResponse{}, nil
	}
	var acknowledged uint32
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	next := make(map[string]egresssink.Record, len(s.pending))
	for eventID, record := range s.pending {
		next[eventID] = record
	}
	for _, eventID := range req.GetEventIds() {
		if _, ok := next[eventID]; ok {
			delete(next, eventID)
			acknowledged++
		}
	}
	if acknowledged > 0 {
		if err := s.persistPendingLocked(next); err != nil {
			return nil, status.Errorf(codes.Unavailable, "persist egress ACK: %v", err)
		}
		s.pending = next
	}
	return &egresspb.AckBytesResponse{Acknowledged: acknowledged}, nil
}

type pendingFile struct {
	Version int                 `json:"version"`
	Records []pendingFileRecord `json:"records"`
}

type pendingFileRecord struct {
	EventID string            `json:"event_id"`
	Record  egresssink.Record `json:"record"`
}

func (s *Server) loadPending() error {
	b, err := os.ReadFile(s.pendingPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read egress replay ledger %s: %w", s.pendingPath, err)
	}
	var file pendingFile
	if err := json.Unmarshal(b, &file); err != nil {
		return fmt.Errorf("decode egress replay ledger %s: %w", s.pendingPath, err)
	}
	if file.Version != 1 {
		return fmt.Errorf("decode egress replay ledger %s: unsupported version %d", s.pendingPath, file.Version)
	}
	for _, row := range file.Records {
		if _, err := uuid.Parse(row.EventID); err != nil || row.Record.InstanceID == "" || row.Record.Minute.IsZero() {
			return fmt.Errorf("decode egress replay ledger %s: invalid record %q", s.pendingPath, row.EventID)
		}
		s.pending[row.EventID] = row.Record
	}
	return nil
}

// persistPendingLocked replaces the ledger through an fsync+rename sequence.
// The caller holds pendingMu, serializing a drain with concurrent ACKs.
func (s *Server) persistPendingLocked(records map[string]egresssink.Record) error {
	if s.pendingPath == "" {
		return nil
	}
	dir := filepath.Dir(s.pendingPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	ids := make([]string, 0, len(records))
	for eventID := range records {
		ids = append(ids, eventID)
	}
	sort.Strings(ids)
	file := pendingFile{Version: 1, Records: make([]pendingFileRecord, 0, len(ids))}
	for _, eventID := range ids {
		file.Records = append(file.Records, pendingFileRecord{EventID: eventID, Record: records[eventID]})
	}
	b, err := json.Marshal(file)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".pending-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) //nolint:errcheck // absent after successful rename
	err = tmp.Chmod(0o600)
	if err == nil {
		_, err = tmp.Write(b)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.pendingPath); err != nil {
		return err
	}
	d, err := os.Open(dir) //nolint:forbidigo // internal ledger directory is opened only to fsync the rename.
	if err != nil {
		return err
	}
	err = d.Sync()
	closeErr = d.Close()
	if err != nil {
		return err
	}
	return closeErr
}
