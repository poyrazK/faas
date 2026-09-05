// capacity_publisher.go — vmmd's live-capacity push to schedd
// (ADR-025 axis 5).
//
// Background. vmmd is the only daemon that owns its host's
// cgroup leaves (per-VM memory.current). Axis 5 closes the
// chooser's stale-store gap: instead of reading the plan-mb
// sum from `instances.ram_mb+8`, the chooser consults a
// per-node in-memory cache that vmmd fills every 1 s.
//
// Wire. vmmd is the gRPC client; schedd is the server. The
// outer reconnect loop dials schedd on
// `deps.scheddTarget` (issue #95 unix:///run/faas/schedd.sock
// default; tcp/dns optional with mTLS), opens a client-stream
// `ReportCapacity`, and writes one CapacityReport per
// CapacityInterval tick. The producer is purely heartbeat-ish:
// if schedd is unreachable, vmmd logs and retries with the
// 1s → 2s → 4s → 8s → 16s → 30s ladder plus 0–500 ms jitter
// (cmd/vmmd/reconnect.go). When the stream returns, the loop
// resets backoff and keeps sending.
//
// Cold-boot safety. If `cfg.ComputeNode.NodeName` is empty
// (single-box dev default), main.go never starts this
// goroutine and vmmd skips the loop entirely — no dial, no
// stream, no report. The schedd-side table stays empty;
// the chooser falls back to
// `store.ComputeNodeUsedMB` (legacy behaviour). ADR-005
// preserved.
//
// Trust model. The publisher does no caching, no decision-
// making, and no policy. It is a pure read sampler → wire
// pump. The schedd-side ledger floor
// (`applyLiveCapacityMB`, PR-2) is the canonical authority;
// a stale-low or hostile vmmd report cannot shrink the live
// accounting.

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"log/slog"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/wire"
)

// CapacityInterval is the publisher's tick cadence. 1 s
// balances freshness (the chooser reads with a 5 s budget)
// against churn (a 200 ms tick would push ~5× as many
// reports on the wire for no freshness gain).
const CapacityInterval = 1 * time.Second

// initialBackoff is the first sleep in the ladder. The
// ladder doubles (1s → 2s → 4s → 8s → 16s → 30s) until
// it hits MaxBackoff. The reset on a successful drain
// returns the next loop iteration to this value.
//
// Why reset on success. A long-lived vmmd that completes
// 30+ successful reports and then hits one transient error
// would otherwise stay at 30 s for the rest of its life —
// the loop accumulates `backoff` across drains even when
// each drain was clean. The gatewayd-internal warmhints publisher
// (cmd/gatewayd-internal/warmhints.go:121) resets on a clean drain;
// we mirror that shape here. (Issue raised in PR-1 review.)
const initialBackoff = 1 * time.Second

// residentBytesFn is the leakcheck seam. Production wires
// `leakcheck.ResidentBytes`; tests inject a stub that
// returns a fixed map. The second return value is the
// "linux-ok" boolean — `false` means we couldn't read
// cgroups (non-Linux dev box, containerized build, etc.),
// so the publisher emits used_mb=0 and the chooser falls
// back to the store sum (ADR-005).
type residentBytesFn func() (map[string]int64, bool)

// countReader is the Manager stub seam. Production wires
// fcvm.Manager; tests inject a stub that returns fixed
// live/leased counts without booting a real Manager (which
// requires /dev/kvm + a fixture cgroup). The interface is
// the load-bearing test seam for end-to-end bufconn tests
// (cmd/vmmd/capacity_publisher_e2e_test.go). Returning a
// 0 from either method is treated as "manager empty" and
// is the same shape the production nil-check implements.
type countReader interface {
	LiveCount() int
	LeasedCount() int
}

// telemetryReader reads vmmd's local Stats snapshot. Production passes the
// already-constructed gRPC server directly, so this is an in-process read;
// the only network operation remains the persistent capacity stream.
type telemetryReader func(context.Context) (*vmmdpb.StatsResponse, error)

// runCapacityPublish is the outer reconnect loop. It is
// invoked as a goroutine from main.go and exits when ctx
// fires. The loop is intentionally simple: dial → stream
// → tick → send → drain-on-error → reconnect. The policy
// lives in schedd's chooser (PR-2); vmmd's only job is to
// keep the stream alive.
//
// Backoff policy. `backoff` starts at `initialBackoff`,
// doubles on each drain failure (1s → 2s → 4s → 8s → 16s
// → 30s capped), and resets to `initialBackoff` after a
// clean drain return (nil error). This matches the
// gatewayd-internal warmhints cadence and prevents a long-lived
// vmmd from getting stuck at the cap after one transient
// error.
//
// TTL removal. The earlier draft had a 5-minute TTL that
// silently exited the loop; removed in PR-1 review. The
// daemon's ctx already terminates the loop on shutdown,
// and a misconfigured environment is caught earlier by
// the empty-target guard at the top.
func runCapacityPublish(
	ctx context.Context,
	counts countReader,
	nodeID string,
	cfg ComputeNodeConfig,
	scheddTarget string,
	scheddClientTLS *tls.Config,
	tick time.Duration,
	resident residentBytesFn,
	nodeKey *ecdsa.PrivateKey,
	nodeKeyID string,
	log *slog.Logger,
	telemetry ...telemetryReader,
) {
	if scheddTarget == "" {
		// No target → no-op. main.go gates this on the
		// NodeName check, but a defensive guard here lets
		// tests inject an empty target without a fatal
		// startup failure.
		return
	}
	if tick <= 0 {
		tick = CapacityInterval
	}
	streamer := prodStreamer{
		scheddTarget:    scheddTarget,
		nodeID:          nodeID,
		scheddClientTLS: scheddClientTLS,
		nodeKey:         nodeKey,
		nodeKeyID:       nodeKeyID,
		log:             log,
	}
	runCapacityPublishWithStreamer(ctx, counts, nodeID, cfg, streamer, tick, resident, log, telemetry...)
}

// runCapacityPublishWithStreamer is the test-friendly entry
// point. Production goes through runCapacityPublish (which
// constructs a prodStreamer); the e2e tests inject a bufconn-
// backed streamer via this seam.
func runCapacityPublishWithStreamer(
	ctx context.Context,
	counts countReader,
	nodeID string,
	cfg ComputeNodeConfig,
	streamer capacityStreamer,
	tick time.Duration,
	resident residentBytesFn,
	log *slog.Logger,
	telemetry ...telemetryReader,
) {
	if tick <= 0 {
		tick = CapacityInterval
	}
	backoff := initialBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		err := drainCapacityPublish(ctx, counts, nodeID, cfg, streamer, tick, resident, log, telemetry...)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			// Clean drain — schedd closed the stream
			// (e.g. graceful restart). Reset the ladder
			// so the next reconnect doesn't sit at 30 s.
			backoff = initialBackoff
		} else {
			log.Warn("vmmd: capacity stream ended; reconnecting",
				"node_id", nodeID, "err", err, "retry_in", backoff.String())
		}
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff, MaxBackoff)
	}
}

// drainCapacityPublish opens one client-streaming
// ReportCapacity session and pushes reports until the
// stream errors or ctx cancels. Returns nil on a clean
// shutdown (ctx cancel); any other error reflects the
// dial failure, send failure, or Recv error and triggers
// the outer reconnect loop.
func drainCapacityPublish(
	ctx context.Context,
	counts countReader,
	nodeID string,
	cfg ComputeNodeConfig,
	streamer capacityStreamer,
	tick time.Duration,
	resident residentBytesFn,
	log *slog.Logger,
	telemetry ...telemetryReader,
) error {
	cli, cleanup, err := streamer.Open(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	log.Info("vmmd: capacity stream connected", "node_id", nodeID)

	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			// Best-effort graceful close: the client stream's
			// CloseAndRecv is deferred by `cleanup`. We don't
			// wait for it here — the ctx is already Done.
			return nil
		case <-t.C:
			report := buildCapacityReport(ctx, streamer, counts, nodeID, cfg, resident, log, telemetry...)
			if err := cli.Send(report); err != nil {
				return err
			}
		}
	}
}

// capacityStreamer is the gRPC-dial seam. Production wires
// the unix-socket path (`openCapacityStream`); tests inject a
// bufconn-backed streamer to drive `runCapacityPublish` end-to-
// end without booting a real /run/faas/schedd.sock (PR-1
// review fix).
type capacityStreamer interface {
	Open(ctx context.Context) (scheddpb.Schedd_ReportCapacityClient, func(), error)
	// SigningKey returns the prodStreamer's node-key +
	// key-id pair so buildCapacityReport can stamp the
	// node_signature field on every report (ADR-053).
	// Pre-slice-3 streamers return nil / "" — the report
	// is emitted unsigned and the schedd accepts it as
	// long as its registry is also nil.
	SigningKey() (*ecdsa.PrivateKey, string)
}

// prodStreamer is the production streamer. It dials schedd
// over a unix (or tcp+TLS) target and opens the client-
// streaming ReportCapacity RPC.
type prodStreamer struct {
	scheddTarget    string
	nodeID          string
	scheddClientTLS *tls.Config
	nodeKey         *ecdsa.PrivateKey
	nodeKeyID       string
	log             *slog.Logger
}

// SigningKey returns the node signing key + key_id pair.
// Used by buildCapacityReport to stamp node_signature on
// every report (ADR-053).
func (p prodStreamer) SigningKey() (*ecdsa.PrivateKey, string) {
	return p.nodeKey, p.nodeKeyID
}

// Open dials schedd and opens a client-streaming ReportCapacity
// session. The returned cleanup func closes the underlying
// conn on drain return. Done as a method on prodStreamer
// rather than a free function so a test can swap the
// capacityStreamer seam in `drainCapacityPublish` (PR-1
// review fix).
func (p prodStreamer) Open(ctx context.Context) (scheddpb.Schedd_ReportCapacityClient, func(), error) {
	// Lazy dial: gRPC's blocking dial happens at first RPC;
	// we want stream-open failures to surface inside the
	// outer reconnect loop's backoff, not at boot.
	conn, err := wire.DialContext(ctx, p.scheddTarget, p.scheddClientTLS)
	if err != nil {
		return nil, nil, err
	}
	cli := scheddpb.NewScheddClient(conn)
	stream, err := cli.ReportCapacity(ctx)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	cleanup := func() {
		// Best-effort: ack the stream so the server's
		// SendAndClose returns. Errors here are expected
		// when ctx is already canceled.
		if _, err := stream.CloseAndRecv(); err != nil {
			p.log.Debug("vmmd: capacity stream close", "node_id", p.nodeID, "err", err)
		}
		_ = conn.Close()
	}
	return stream, cleanup, nil
}

// buildCapacityReport samples the live count + cgroup
// memory.current and returns a typed proto. UsedMB is the
// sum of all instance cgroup memory.current values, NOT a
// plan-mb or sample-rate approximation; the chooser applies
// the ledger floor as a separate check (PR-2).
//
// RAMHeadroomMB is `cfg.ComputeNode.MemMB - usedMB`,
// clamped at 0. A negative headroom (over-commit defence
// miss) surfaces as 0 on the wire so the chooser can
// see "saturated" without parsing a negative value.
//
// vcpu_busy is filled as `live * 2` (per-vCPU-2 default).
// Per-cgroup-weight-sum is a v1.1 upgrade; the placeholder
// is conservative and matches the §4.5 future-work note
// in ADR-025.
//
// Slice-3 (ADR-053): when the streamer has a node signing
// key + key_id, the report carries a 64-byte raw (r||s)
// ECDSA-P-256 signature over the canonical payload. Pre-slice-3
// streamers (nil key / empty key_id) emit an empty signature
// — the wire field is additive, so legacy schedd silently
// accepts and slice-3 schedd (when configured with a
// registry) rejects the stream.
//
// Sign failures are logged + the report is emitted unsigned.
// A persistent signing-key bug must surface in `vmmd` logs,
// not silently regress to "sends zero signatures".
func buildCapacityReport(
	ctx context.Context,
	streamer capacityStreamer,
	counts countReader,
	nodeID string,
	cfg ComputeNodeConfig,
	resident residentBytesFn,
	log *slog.Logger,
	telemetry ...telemetryReader,
) *scheddpb.CapacityReport {
	// nil counts → live=0, leased=0. Lets the unit tests
	// run without a real *fcvm.Manager (which requires
	// /dev/kvm). Production always passes a non-nil
	// countReader because main.go constructs the Manager
	// before the publisher goroutine starts.
	var live, leased int32
	if counts != nil {
		live = int32(counts.LiveCount())
		leased = int32(counts.LeasedCount())
	}

	var usedMB int64
	var stats *vmmdpb.StatsResponse
	if len(telemetry) > 0 && telemetry[0] != nil {
		var err error
		stats, err = telemetry[0](ctx)
		if err != nil && log != nil {
			log.Warn("vmmd: telemetry snapshot failed; sending capacity only", "node_id", nodeID, "err", err)
		}
	}
	if stats != nil && stats.GetTotalResidentBytes() != nil {
		usedMB = stats.GetTotalResidentBytes().GetValue() >> 20
	} else if bytes, ok := resident(); ok {
		var sum int64
		for _, b := range bytes {
			sum += b
		}
		usedMB = sum >> 20 // bytes → MiB (1024×1024)
	}

	headroom := int64(cfg.MemMB) - usedMB
	if headroom < 0 {
		headroom = 0
	}
	// Avoid overflow on the int32 typed wire field. The
	// chooser applies the floored int64 in PR-2; here we
	// just clamp the wire representation.
	usedMB = clampInt32(usedMB)
	headroom = clampInt32(headroom)

	report := &scheddpb.CapacityReport{
		NodeId:          nodeID,
		SampledAtUnixMs: time.Now().UnixMilli(),
		LiveCount:       live,
		LeasedCount:     leased,
		UsedMb:          int32(usedMB),
		RamHeadroomMb:   int32(headroom),
		VcpuBusy:        live * 2,
	}
	if stats != nil {
		report.Instances = make([]*scheddpb.InstanceTelemetry, 0, len(stats.GetInstances()))
		for _, in := range stats.GetInstances() {
			if in == nil || in.GetInstance() == "" {
				continue
			}
			row := &scheddpb.InstanceTelemetry{
				InstanceId:          in.GetInstance(),
				ResidentBytes:       in.GetResidentBytes(),
				CpuPct:              in.GetCpuPct(),
				CpuSeconds:          in.GetCpuSeconds(),
				CpuThrottledSeconds: in.GetCpuThrottledSeconds(),
				InflightRequests:    in.GetInflightRequests(),
				LastRequestAt:       in.GetLastRequestAt(),
				NetTxBytes:          in.GetNetTxBytes(),
				NetRxBytes:          in.GetNetRxBytes(),
				OpenConns:           in.GetOpenConns(),
			}
			report.Instances = append(report.Instances, row)
		}
	}
	// Slice-3: stamp node_signature + node_key_id when the
	// streamer was wired with a signing key. Pre-slice-3
	// streamers (nil key) leave both fields empty — additive.
	if streamer != nil {
		if key, keyID := streamer.SigningKey(); key != nil && keyID != "" {
			schedReport := sched.CapacityReport{
				NodeID:        report.NodeId,
				SampledAt:     time.UnixMilli(report.SampledAtUnixMs),
				LiveCount:     report.LiveCount,
				LeasedCount:   report.LeasedCount,
				UsedMB:        report.UsedMb,
				RAMHeadroomMB: report.RamHeadroomMb,
				VCPUBusy:      report.VcpuBusy,
			}
			sig, err := sched.SignNodeReport(key, schedReport)
			if err != nil {
				// Sign failure is rare (crypto/rand) but
				// must not silently regress to unsigned.
				// Emit a one-shot-style log; the report is
				// sent unsigned and schedd (if signature-
				// strict) will reject.
				if log != nil {
					log.Warn("vmmd: capacity sign failed; sending unsigned",
						"node_id", nodeID, "err", err)
				}
			} else {
				report.NodeSignature = sig
				report.NodeKeyId = keyID
			}
		}
	}
	return report
}

// clampInt32 caps v at the int32 max. Used to avoid
// overflow on wire fields typed int32. (PR-1 review.)
func clampInt32(v int64) int64 {
	if v > 1<<31-1 {
		return 1<<31 - 1
	}
	return v
}
