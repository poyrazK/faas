package instancestats

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// DefaultStatsInterval is the per-Tick cadence (issue #170 / PR-A).
// 200 ms = 5 Hz; the 250 ms spike-capture acceptance gate in issue
// #170's metal test passes at one or two ticks. A future #172 config
// knob (StatsInterval) will plumb this through cmd/schedd without
// touching the Reader API.
const DefaultStatsInterval = 200 * time.Millisecond

// DefaultFreshness is the staleness budget used by Reader's signal
// accessors. Rows remain available through snapshot accessors for
// diagnostics, but stale rows cannot drive reactive scaling.
const DefaultFreshness = 5 * time.Second

// Dialer is the per-tick per-node VMM transport factory. Mirrors
// pkg/sched.HeartbeatDialer (issue #120 / PR #122): the poller
// dials fresh per Tick and closes the VMM when the sweep is done,
// so vmmd conn churn is bounded by the dialer. cmd/schedd passes
// HeartbeatDialerFunc(deps.dialVMM) so the production closure
// (overlay.Dial) is reused bit-for-bit — no second dial primitive
// per call site.
type Dialer interface {
	Dial(ctx context.Context, targetURL string, tlsCfg *tls.Config) (sched.VMM, error)
}

// DialerFunc adapts an ordinary function to the Dialer interface.
// Same precedent as HeartbeatDialerFunc in pkg/sched/heartbeat.go.
type DialerFunc func(ctx context.Context, targetURL string, tlsCfg *tls.Config) (sched.VMM, error)

// Dial implements Dialer.
func (f DialerFunc) Dial(ctx context.Context, targetURL string, tlsCfg *tls.Config) (sched.VMM, error) {
	return f(ctx, targetURL, tlsCfg)
}

// Poller is the periodic instance-stats worker. Mirrors
// pkg/sched.Heartbeat in shape: Tick does one full sweep; Run
// loops Tick on a fixed interval until ctx is done. Per-instance
// state lives in the Reader; the poller itself is stateless across
// ticks so process restart has no plumbing consequence.
type Poller struct {
	Interval  time.Duration
	Store     state.Store
	Dialer    Dialer
	TLS       *tls.Config
	Reader    *Reader
	Metrics   *wire.OpsMetrics
	Log       *slog.Logger
	Now       func() time.Time
	Freshness time.Duration
	// Telemetry is the notification/stream-backed node observer cache. When
	// set, Tick only projects its local snapshot and never dials vmmd.
	Telemetry *sched.NodeTelemetryCache
	// NodeRegistry is the same notification-backed active-node snapshot used
	// by placement and heartbeat. Nil preserves the legacy store lookup.
	NodeRegistry *sched.NodeRegistry
}

// WithTelemetry switches the poller to the persistent node telemetry stream.
func (p *Poller) WithTelemetry(cache *sched.NodeTelemetryCache) *Poller {
	if p != nil {
		p.Telemetry = cache
	}
	return p
}

// WithNodeRegistry removes the per-tick ActiveComputeNodes query from the
// production observer path.
func (p *Poller) WithNodeRegistry(reg *sched.NodeRegistry) *Poller {
	if p != nil {
		p.NodeRegistry = reg
	}
	return p
}

// NewPoller builds a Poller with sensible defaults applied to
// zero-valued fields. Callers should set Store / Dialer / Reader
// / Metrics; the rest have safe defaults (200 ms interval, real
// time.Now, real slog default logger).
func NewPoller(store state.Store, dialer Dialer, tlsCfg *tls.Config, reader *Reader, metrics *wire.OpsMetrics, log *slog.Logger) *Poller {
	if log == nil {
		log = slog.Default()
	}
	return &Poller{
		Interval:  DefaultStatsInterval,
		Store:     store,
		Dialer:    dialer,
		TLS:       tlsCfg,
		Reader:    reader,
		Metrics:   metrics,
		Log:       log,
		Now:       time.Now,
		Freshness: DefaultFreshness,
	}
}

// TickInterval returns the poller's interval. pkg/sched.Loop's
// WithInstanceStats option needs this to size its ticker.
func (p *Poller) TickInterval() time.Duration {
	if p.Interval <= 0 {
		return DefaultStatsInterval
	}
	return p.Interval
}

// Run blocks until ctx is done, ticking on the configured
// interval. The first Tick is invoked before the ticker fires so
// the first sample lands at t=0 (time.NewTicker does not fire
// immediately; this is a documented correction to the heartbeat
// loop's "first sample at t=Interval" behaviour, see issue #120).
func (p *Poller) Run(ctx context.Context) error {
	if err := p.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
		// First-tick failure is logged but does not abort the
		// loop — partial sweeps are still useful, and the next
		// tick has a fresh chance.
		p.Log.Warn("instance stats first tick failed", "err", err)
	}
	t := time.NewTicker(p.TickInterval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := p.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
				p.Log.Warn("instance stats tick failed", "err", err)
			}
		}
	}
}

// Tick performs one full sweep: list the active-node snapshot, list live
// instances, and project either the persistent telemetry cache or the legacy
// fresh-dial path into InstanceStat rows. The production path has no per-node
// network calls here; legacy dial failures remain partial and non-fatal.
func (p *Poller) Tick(ctx context.Context) error {
	started := p.now()
	var nodes []state.ComputeNode
	if p.NodeRegistry != nil {
		nodes = p.NodeRegistry.Snapshot()
	} else {
		var err error
		nodes, err = p.Store.ActiveComputeNodes(ctx)
		if err != nil {
			return err
		}
	}
	instances, err := p.Store.ListAllInstances(ctx)
	if err != nil {
		return err
	}
	// Group instances by node for the join.
	byNode := make(map[string][]state.Instance, len(nodes))
	for _, in := range instances {
		if in.NodeID == "" {
			continue
		}
		byNode[in.NodeID] = append(byNode[in.NodeID], in)
	}
	// Issue #463 / ADR-070 / PR-C: pre-load the per-deployment
	// sidecar RAM slice ONCE per Tick (rather than once per
	// instance) so a 100-instance fleet with 5 deployments is
	// 5 DB reads per minute, not 100. The map is dense on
	// deploymentID; an instance whose deployment_id is absent
	// (= legacy no-sidecars deploy or a transient cache miss)
	// falls back to nil, which the sampler collapses to the
	// legacy single-arg admission form.
	sidecarByDeploy := make(map[string][]int, len(byNode))
	for _, in := range instances {
		if in.DeploymentID == "" {
			continue
		}
		if _, seen := sidecarByDeploy[in.DeploymentID]; seen {
			continue
		}
		mbs, err := p.Store.DeploymentSidecarRAMs(ctx, in.DeploymentID)
		if err != nil {
			// Fail-closed: leave the deployment entry absent;
			// downstream admission reverts to the no-sidecar
			// form. The next tick retries.
			p.Log.Warn("instance stats: deployment sidecar RAM lookup failed",
				"deployment_id", in.DeploymentID, "err", err)
			continue
		}
		sidecarByDeploy[in.DeploymentID] = mbs
	}
	// Production uses the persistent vmmd→schedd capacity stream. The stream
	// updates one complete batch per node; this local projection can continue at
	// the existing 200 ms cadence without opening any per-node connections.
	if p.Telemetry != nil {
		rows, rolled := p.decodeTelemetrySnapshot(
			p.Telemetry.Snapshot(p.now()), instances, sidecarByDeploy)
		p.Reader.Replace(rows)
		if p.Metrics != nil {
			p.Metrics.ReplaceInstanceStats(rolled, p.now().Sub(started))
		}
		return nil
	}
	rows := make([]InstanceStat, 0, len(instances))
	rolled := make([]wire.InstanceStatRow, 0, len(instances))
	for _, node := range nodes {
		nodeRows, nodeRolled := p.tickNode(ctx, node, byNode[node.ID], sidecarByDeploy)
		rows = append(rows, nodeRows...)
		rolled = append(rolled, nodeRolled...)
	}
	// Replace is atomic; readers see either the previous snapshot
	// or the next, never a torn mix.
	p.Reader.Replace(rows)
	// Metrics rollup: max CPU / sum RSS / sum inflight per
	// (app, node). The wire side collapses NaN for absent
	// values; instancestats passes NaN through so the rollup
	// excludes them.
	if p.Metrics != nil {
		p.Metrics.ReplaceInstanceStats(rolled, p.now().Sub(started))
	}
	return nil
}

// decodeTelemetrySnapshot joins a persistent node batch with durable instance
// state. App/deployment ownership remains sourced from schedd's store, while
// all vmmd measurements arrive together over ReportCapacity.
func (p *Poller) decodeTelemetrySnapshot(
	snapshot []sched.NodeTelemetryWithNode,
	instances []state.Instance,
	sidecarByDeploy map[string][]int,
) ([]InstanceStat, []wire.InstanceStatRow) {
	byID := make(map[string]state.Instance, len(instances))
	for _, instance := range instances {
		byID[instance.ID] = instance
	}
	rows := make([]InstanceStat, 0, len(snapshot))
	rolled := make([]wire.InstanceStatRow, 0, len(snapshot))
	for _, cached := range snapshot {
		in := cached.Telemetry
		if in.InstanceID == "" {
			continue
		}
		durable, ok := byID[in.InstanceID]
		if !ok {
			continue
		}
		row := InstanceStat{
			InstanceID:       in.InstanceID,
			NodeID:           cached.NodeID,
			AppID:            durable.AppID,
			InflightRequests: in.InflightRequests,
			CPU:              Unknown,
			RSS:              Unknown,
			// Preserve the vmmd report timestamp. Using the local
			// projection time here would make a stalled telemetry
			// stream look fresh to the autoscaler.
			SampledAt:  cached.SampledAt,
			SidecarMBs: sidecarByDeploy[durable.DeploymentID],
		}
		wireRow := wire.InstanceStatRow{
			AppID:            durable.AppID,
			NodeID:           cached.NodeID,
			InflightRequests: in.InflightRequests,
			CPUPct:           math.NaN(),
			RSSMB:            math.NaN(),
			CPUSeconds:       math.NaN(),
			ThrottledUsec:    math.NaN(),
		}
		if in.CPUPct != nil {
			row.CPUPct = *in.CPUPct
			row.CPU = Valid
			wireRow.CPUPct = *in.CPUPct
		}
		if in.CPUSeconds != nil {
			row.CPUUsageUsec = uint64(*in.CPUSeconds * 1e6)
			row.CPUHour = *in.CPUSeconds / 3600.0
			wireRow.CPUSeconds = *in.CPUSeconds
		}
		if in.CPUThrottledSeconds != nil {
			wireRow.ThrottledUsec = *in.CPUThrottledSeconds * 1e6
		}
		if in.NetTxBytes != nil {
			row.TXBytes = uint64(*in.NetTxBytes)
			row.TX = Valid
		}
		if in.ResidentBytes != nil {
			mib := float64(*in.ResidentBytes) / float64(1024*1024)
			row.RSSMB = mib
			row.RSS = Valid
			wireRow.RSSMB = mib
		}
		if !in.LastRequestAt.IsZero() {
			row.LastRequestAt = in.LastRequestAt
		} else {
			row.LastRequestAt = durable.LastRequestAt
		}
		rows = append(rows, row)
		rolled = append(rolled, wireRow)
	}
	return rows, rolled
}

// tickNode dials one node fresh, calls Stats, and decodes the
// result into InstanceStat rows + wire rollup rows. On dial
// failure it logs, increments the per-node error counter, and
// returns empty slices — the caller continues to the next node.
func (p *Poller) tickNode(ctx context.Context, node state.ComputeNode, siblings []state.Instance, sidecarByDeploy map[string][]int) ([]InstanceStat, []wire.InstanceStatRow) {
	if p.Dialer == nil {
		return nil, nil
	}
	vmm, err := p.Dialer.Dial(ctx, node.TargetURL, p.TLS)
	if err != nil {
		p.Log.Warn("instance stats dial failed", "node_id", node.ID, "err", err)
		if p.Metrics != nil {
			p.Metrics.InstanceStatsPartialError(node.ID)
		}
		return nil, nil
	}
	defer func() { _ = vmm.Close() }()
	snap, err := vmm.Stats(ctx)
	if err != nil {
		p.Log.Warn("instance stats vmm.Stats failed", "node_id", node.ID, "err", err)
		if p.Metrics != nil {
			p.Metrics.InstanceStatsPartialError(node.ID)
		}
		return nil, nil
	}
	// Index durable sibling state by instance id for the join.
	// The poller uses state.Instance.LastRequestAt as the
	// fallback for LastRequestAt when the wire is zero or absent.
	sibByID := make(map[string]state.Instance, len(siblings))
	for _, in := range siblings {
		sibByID[in.ID] = in
	}
	now := p.now()
	rows := make([]InstanceStat, 0, len(snap.Instances))
	rolled := make([]wire.InstanceStatRow, 0, len(snap.Instances))
	for _, in := range snap.Instances {
		if in.InstanceID == "" {
			continue
		}
		durable, ok := sibByID[in.InstanceID]
		if !ok {
			// Wire reported an instance we have no
			// state for (e.g. concurrent destroy). Skip
			// rather than publish a row with empty
			// AppID — that would silently land in the
			// rollup with the wrong (app, node) tuple.
			continue
		}
		row := InstanceStat{
			InstanceID:       in.InstanceID,
			NodeID:           node.ID,
			AppID:            durable.AppID,
			InflightRequests: in.InflightRequests,
			CPU:              Unknown,
			RSS:              Unknown,
			SampledAt:        now,
			// Issue #463 / ADR-070 / PR-C: sidecar RAM slice
			// from the pre-loaded cache. Nil when the
			// deployment row has no sidecars or the lookup
			// failed — both collapse to the legacy no-sidecar
			// admission form on the meterd side.
			SidecarMBs: sidecarByDeploy[durable.DeploymentID],
		}
		if in.RequestCountTotal != nil && *in.RequestCountTotal >= 0 {
			row.RequestCountTotal = uint64(*in.RequestCountTotal)
			row.RequestCountValid = true
		}
		wireRow := wire.InstanceStatRow{
			AppID:            durable.AppID,
			NodeID:           node.ID,
			InflightRequests: in.InflightRequests,
			CPUPct:           math.NaN(),
			RSSMB:            math.NaN(),
			CPUSeconds:       math.NaN(),
			ThrottledUsec:    math.NaN(),
		}
		// CPU: the wire is a CPUPct *float64. PR-A treats
		// nil as the "absent this tick" sentinel — the
		// poller stamps Unknown and the wire rollup excludes
		// the row. There is no previous-sample baseline in
		// PR-A; cumulative-counter regression detection
		// (usage_usec going backwards) lives in PR-B's
		// Stats handler (it can correlate with a firecracker
		// rebuild on the vmmd side). PR-A's own contract is
		// narrower: nil → Unknown, non-nil → Valid.
		if in.CPUPct != nil {
			// The wire is DoubleValue: the wrapper is
			// nil when absent, populated when present.
			// vmmd emits CPUPct as a *float64 read by the
			// Stats handler from cgroupstats.Sample
			// (PR-A wire) or a future client-side rate
			// (post-#170 followup). Today the schedd
			// side receives CPUPct=nil because vmmd does
			// not populate it yet. We respect the
			// contract: nil → Unknown, non-nil → Valid.
			wireRow.CPUPct = *in.CPUPct
			row.CPUPct = *in.CPUPct
			row.CPU = Valid
		}
		// CPUSeconds (issue #279 / PR-B): cumulative
		// CPU-seconds from vmmd's cpustats cache. nil
		// means no baseline yet; we leave the wire field
		// as NaN so the rollup's regression-handling
		// branch does not see a bogus reading. Otherwise
		// we propagate the value verbatim: vmmd owns the
		// regression detection and resets the cache
		// baseline on cgroup recreation, so the wire is
		// always a valid cumulative value.
		if in.CPUSeconds != nil {
			wireRow.CPUSeconds = *in.CPUSeconds
			row.CPUUsageUsec = uint64(*in.CPUSeconds * 1e6)
			row.CPUHour = *in.CPUSeconds / 3600.0
		}
		// CpuThrottledSeconds (issue #301 / ADR-043): cumulative
		// CPU-throttled-seconds from vmmd's cpustats cache. nil
		// means no baseline yet — the poller leaves ThrottledUsec
		// at NaN so the rollup's regression-handling branch
		// excludes the row (matches the CPUSeconds contract).
		// Wire unit is seconds; the rollup converts to seconds
		// (× 1e6 → usec) for the cpuThrottleLastSeen baseline
		// comparison. Without this decode, the per-(account_id,
		// app_id) vmmd_cpu_throttle_seconds_total counter is
		// never fed (PR #390 review finding #2, ship-blocker).
		if in.CpuThrottledSeconds != nil {
			wireRow.ThrottledUsec = *in.CpuThrottledSeconds * 1e6
		}
		// NetTxBytes (ADR-046, step 7): per-tick byte delta
		// on root-side vethHost.rx_bytes from vmmd's
		// netstats cache. Wrapper so "absent" means "first
		// sample after cache reset OR cgroup regression
		// (veth recreation detected by netstats.Cache)" —
		// distinct from a real 0-byte delta. The poller
		// stamps TX=Unknown on absent; only meterd's
		// SampleAndRoll consumes this row (pkg/meter/
		// sampler.go, PR-2 fold-in). Future readers (the
		// reaper, the dashboard) can ignore it.
		//
		// The unsigned→signed cast on the wire cannot
		// produce a negative value: cmd/vmmd/network_poller.go
		// clamps `rx > math.MaxInt64` to math.MaxInt64 before
		// passing the reading into the cache. So `*in.NetTxBytes
		// < 0` is unreachable — the vmmd stream can only emit
		// nil (absent) or a non-negative int64. Treat any
		// absent wrapper as Unknown; present as Valid.
		if in.NetTxBytes != nil {
			row.TXBytes = uint64(*in.NetTxBytes)
			row.TX = Valid
		}
		// NetRxBytes (ADR-048): per-tick byte delta on
		// root-side vethHost.tx_bytes — mirror of NetTxBytes
		// on the ingress direction (root → guest). Same
		// unsigned→signed + nil-wrapper contract as egress:
		// vmmd clamps tx_bytes to math.MaxInt64 before
		// shipping, so the wrapper is only nil when the
		// netstats cache has regressed / never observed /
		// missed — the poller stamps RX=Unknown and the
		// meterd sampler (when wired) skips the row. The
		// wire field awaits `make proto` regen (PR-A commit
		// #2 follow-up); today NetRxBytes is always nil on
		// the typed wire mirror, RX stays Unknown, and the
		// sampler writes 0 to usage_minutes.net_rx_bytes
		// (safe under additive-merge).
		if in.NetRxBytes != nil {
			row.RXBytes = uint64(*in.NetRxBytes)
			row.RX = Valid
		}
		// RSS: wire sends *int64. nil → Unknown; non-nil →
		// convert bytes → MiB.
		if in.ResidentBytes != nil {
			mib := float64(*in.ResidentBytes) / float64(1024*1024)
			wireRow.RSSMB = mib
			row.RSSMB = mib
			row.RSS = Valid
		}
		// LastRequestAt: prefer wire (PR-B will populate
		// from ActivityTracker); fall back to durable
		// state.Instance.LastRequestAt when the wire is
		// zero.
		switch {
		case !in.LastRequestAt.IsZero():
			row.LastRequestAt = in.LastRequestAt
		case !durable.LastRequestAt.IsZero():
			row.LastRequestAt = durable.LastRequestAt
		}
		rows = append(rows, row)
		rolled = append(rolled, wireRow)
	}
	return rows, rolled
}

// now returns the poller's wall clock. Defaults to time.Now so
// tests can inject a fake clock via Poller.Now.
func (p *Poller) now() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}
