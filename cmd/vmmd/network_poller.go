// PR-2 egress-byte poll adapter (ADR-046).
//
// This file owns the loop that reads the kernel byte counter at
// `/sys/class/net/<vethHost>/statistics/rx_bytes` for every live
// instance and feeds the per-instance deltas into
// pkg/fcvm/netstats.Cache. The cache's regression-safe semantics
// turn the raw cumulative counter into a per-tick delta that
// vmmd's Stats gRPC handler can serve on the wire without doing
// sysfs I/O on the hot path.
//
// Counter source (ADR-046 §1):
//
//	root ns:    br-tenants ── vethHost
//	netns:      vethPeer ── tap0 ── guest
//
// Customer egress traverses `tap0 → vethPeer → vethHost`. On
// root-side `vethHost` this is **RX**. Reading vethHost.tx_bytes
// would count gateway → guest (ingress), not customer egress. The
// kernel-counter file is
// `/sys/class/net/<vethHost>/statistics/rx_bytes`. The per-plan
// `tc tbf` qdisc on vethPeer shapes these same packets, so the cap
// and the meter cover the same traffic.
//
// # Design notes (mirror pkg/fcvm/cpustats and cmd/vmmd/cpu_poller.go)
//
//   - The poll cadence is 250 ms — matches the cpustats cache
//     cadence and the schedd instancestats.Poller cadence so
//     vmmd's per-tick deltas line up with schedd's per-tick
//     read window. A looser cadence widens the per-minute drift
//     tolerance; a tighter cadence doubles the sysfs I/O for no
//     observability gain.
//
//   - The SnapshotLive dependency is the Manager.SnapshotLive
//     accessor (ADR-046 / step 6): it returns a fresh
//     (instanceID → vethHost) map under m.mu, so a concurrent
//     Destroy / Wake cannot race the iteration. The map is owned
//     by the caller; the poller does NOT mutate it.
//
//   - The readRXBytes dependency is a function-typed seam so the
//     unit test can swap it for an in-memory stub without
//     touching sysfs. The default is netns.ReadVethRXBytes (a
//     small wrapper over os.ReadFile on the kernel counter
//     file); non-metal builds get a stub that returns 0.
//
//   - The cache handles regression on its own (veth recreation):
//     pkg/fcvm/netstats.Cache.Observe returns ok=false on
//     regression and resets the baseline. The poller just
//     records the observation; the cache decides whether to emit.
//
//   - Poll failures (per-instance sysfs read errors) are logged
//     at Warn and counted via ops.EgressSourceErrors. The cache
//     baseline for that instance stays put — the next successful
//     tick picks up where the failed one left off. A persistent
//     failure shows up in the counter as the alert source.
package main

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/netstats"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
)

// EgressBytesPollInterval is the cadence of the veth byte
// scrape loop. 250 ms matches the cpustats cache cadence and
// the schedd instancestats.Poller cadence.
const EgressBytesPollInterval = 250 * time.Millisecond

// snapshotLiveFunc is the Manager.SnapshotLive seam. Production
// passes mgr.SnapshotLive; tests inject a stub.
type snapshotLiveFunc func() map[string]string

// readRXBytesFunc is the per-instance sysfs read seam. Production
// passes netns.ReadVethRXBytes; tests inject a stub.
type readRXBytesFunc func(vethHost string) (uint64, error)

// readTXBytesFunc is the per-instance sysfs read seam for the
// ingress direction (ADR-048). Production passes
// netns.ReadVethTXBytes; tests inject a stub.
type readTXBytesFunc func(vethHost string) (uint64, error)

// runNetworkEgressPoll starts the per-instance veth byte poll
// goroutine and returns when ctx is cancelled. It is launched
// alongside runEgressPoll and runCPUSampleLoop from main.go.
//
// The snapshotLive parameter is the function-typed seam; pass
// nil to use the production mgr.SnapshotLive. The readRXBytes
// parameter is the per-instance sysfs read seam; pass nil to
// use the production netns.ReadVethRXBytes (added in step 6;
// the function pointer contract mirrors netns.PopCounters in
// cmd/vmmd/poller.go).
//
// The cache is mutated under its own mutex; the poller never
// holds any lock across a sysfs read. The order per tick is:
//  1. Snapshot the live instances (returns a fresh map copy).
//  2. For each (instanceID, vethHost), read rx_bytes.
//  3. Hand the reading to netstats.Cache.Observe, which
//     computes the per-instance delta and exposes it to the
//     Stats gRPC handler via Lookup.
//
// A successful poll emits no metric directly — the per-tick
// delta is read by the Stats handler when schedd asks. The
// only Prometheus hook in this loop is EgressSourceErrors on
// per-instance read failures.
func runNetworkEgressPoll(
	ctx context.Context,
	mgr *fcvm.Manager,
	cache *netstats.Cache,
	ops *wire.OpsMetrics,
	snapshotLive snapshotLiveFunc,
	readRXBytes readRXBytesFunc,
	readTXBytes readTXBytesFunc,
	interval time.Duration,
	log *slog.Logger,
) {
	if snapshotLive == nil && mgr != nil {
		snapshotLive = mgr.SnapshotLive
	}
	if readRXBytes == nil {
		readRXBytes = netns.ReadVethRXBytesForPoll
	}
	if readTXBytes == nil {
		readTXBytes = netns.ReadVethTXBytesForPoll
	}
	if interval <= 0 {
		interval = EgressBytesPollInterval
	}
	if cache == nil {
		log.Error("network egress poll: nil cache, exiting goroutine")
		return
	}
	if ops == nil {
		log.Error("network egress poll: nil ops, exiting goroutine")
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	var failures pollFailureLog
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if snapshotLive == nil {
				continue
			}
			now := time.Now()
			snapshot := snapshotLive()
			for instanceID, vethHost := range snapshot {
				// I2 (review): re-check context between
				// per-instance sysfs reads so graceful
				// shutdown does not stall past
				// systemd's 30 s TimeoutStopSec on a
				// box with hundreds of live instances.
				if ctx.Err() != nil {
					return
				}
				if vethHost == "" {
					continue
				}
				rx, err := readRXBytes(vethHost)
				if err != nil {
					if failures.failed(instanceID+"/rx", err.Error()) {
						log.Warn("egress rx_bytes read failed",
							"instance", instanceID,
							"veth", vethHost,
							"err", err)
					}
					if c := ops.EgressSourceErrors(); c != nil {
						c.Inc()
					}
					continue
				}
				if failures.succeeded(instanceID + "/rx") {
					log.Info("egress rx_bytes read recovered", "instance", instanceID, "veth", vethHost)
				}
				// I6 (review): clamp the kernel
				// counter to math.MaxInt64 before
				// downstream int64 casts. The
				// wire / store column is signed
				// (wrapperspb.Int64Value /
				// usage_minutes bigint); a wrap
				// on the unsigned-→signed cast
				// would look like a regression
				// and silently zero future
				// deltas (the cache drops the
				// baseline on any non-monotonic
				// step). Practically unreachable
				// on a 1 Gbit uplink, but the
				// guard is cheap.
				if rx > math.MaxInt64 {
					log.Warn("egress rx_bytes exceeds int64 range; clamping",
						"instance", instanceID,
						"veth", vethHost,
						"rx", rx)
					rx = math.MaxInt64
				}
				// ADR-048: read the tx_bytes counter in the same
				// tick so the cache's parallel ingress baseline
				// stays in lockstep with the egress baseline. A
				// tx read failure does not invalidate the rx
				// reading — we Observe with rx populated and
				// tx=0, and the cache's tx baseline regresses
				// (it sees 0 < prev.tx), drops the tx
				// baseline, and re-establishes it on the next
				// successful tick. The egress direction is
				// unaffected.
				tx, txErr := readTXBytes(vethHost)
				if txErr != nil {
					if failures.failed(instanceID+"/tx", txErr.Error()) {
						log.Warn("ingress tx_bytes read failed",
							"instance", instanceID,
							"veth", vethHost,
							"err", txErr)
					}
					if c := ops.EgressSourceErrors(); c != nil {
						c.Inc()
					}
					// tx stays 0 — see ADR-048 §3.2 on
					// tx baseline drop policy.
				} else {
					if failures.succeeded(instanceID + "/tx") {
						log.Info("ingress tx_bytes read recovered", "instance", instanceID, "veth", vethHost)
					}
					if tx > math.MaxInt64 {
						log.Warn("ingress tx_bytes exceeds int64 range; clamping",
							"instance", instanceID,
							"veth", vethHost,
							"tx", tx)
						tx = math.MaxInt64
					}
				}
				cache.Observe(netstats.Observation{
					InstanceID: instanceID,
					RXBytes:    rx,
					TXBytes:    tx,
					At:         now,
				})
			}
			// I1 (review): evict cache entries for
			// instances that vanished since the
			// previous tick. Manager.Park deletes
			// m.live without going through the
			// vmmdgrpc.Destroy handler, so the
			// explicit ForgetNet on teardown is
			// bypassed. Without this sweep the
			// cache grows unbounded across
			// park/park/park cycles. The
			// snapshot map is fresh per tick; we
			// drop any cache key whose instance
			// is not in the snapshot. The cost
			// is O(N) on a transient set per
			// tick — bounded by the live set
			// size.
			evictStaleCacheEntries(cache, snapshot)
			failures.retain(func(key string) bool {
				instanceID, _, _ := strings.Cut(key, "/")
				_, live := snapshot[instanceID]
				return live
			})
		}
	}
}

// evictStaleCacheEntries drops cache entries whose instance id is
// not in `live`. Called once per tick from runNetworkEgressPoll so
// instances that bypass the vmmdgrpc.Destroy handler (notably
// Manager.Park, which deletes m.live without going through the
// gRPC server) still see their baseline removed. The cache's own
// Diff method holds the cache mutex once and returns the stale
// ids; this function iterates them outside the lock and calls
// Forget per-id (each Forget takes the mutex briefly — O(N)
// work is fine because the live set is bounded by
// max_concurrency × apps).
func evictStaleCacheEntries(cache *netstats.Cache, live map[string]string) {
	if cache == nil || len(live) == 0 {
		return
	}
	liveSet := make(map[string]struct{}, len(live))
	for id := range live {
		liveSet[id] = struct{}{}
	}
	for _, stale := range cache.Diff(liveSet) {
		cache.Forget(stale)
	}
}
