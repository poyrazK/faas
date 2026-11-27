package main

import (
	"context"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// resourceSlotWire is the type=0x0C JSON body (ADR-20261121170000).
// Mirrors guest/init/resource_slots_linux.go.
type resourceSlotWire struct {
	Samples           int   `json:"samples"`
	MemStartMB        int   `json:"mem_start_mb"`
	MemPeakMB         int   `json:"mem_peak_mb"`
	MemEndMB          int   `json:"mem_end_mb"`
	MemLimitMB        int   `json:"mem_limit_mb,omitempty"`
	CPUUsec           int64 `json:"cpu_usec"`
	CPUPeakMillicores int   `json:"cpu_peak_millicores"`
	CPULimitMillis    int   `json:"cpu_limit_millicores,omitempty"`
	PeakAgeMS         int64 `json:"peak_age_ms"`
}

const (
	// resourceSlotMaxMB bounds every memory value: no VM is this large.
	resourceSlotMaxMB = 1 << 20
	// resourceSlotMaxMillicores bounds the CPU rates: 256 vCPUs.
	resourceSlotMaxMillicores = 256_000
)

// valid refuses values no real slot can have, so a buggy or hostile guest
// cannot store nonsense.
func (w resourceSlotWire) valid() bool {
	inMB := func(v int) bool { return v >= 0 && v <= resourceSlotMaxMB }
	slotMS := api.RouteResourceSlotInterval.Milliseconds()
	return w.Samples > 0 && w.Samples <= int(2*slotMS/api.RouteResourceSampleInterval.Milliseconds()) &&
		inMB(w.MemStartMB) && inMB(w.MemPeakMB) && inMB(w.MemEndMB) && inMB(w.MemLimitMB) &&
		w.MemPeakMB >= w.MemStartMB && w.MemPeakMB >= w.MemEndMB &&
		w.CPUUsec >= 0 && w.CPUUsec <= 2*slotMS*1000*resourceSlotMaxMillicores/1000 &&
		w.CPUPeakMillicores >= 0 && w.CPUPeakMillicores <= resourceSlotMaxMillicores &&
		w.CPULimitMillis >= 0 && w.CPULimitMillis <= resourceSlotMaxMillicores &&
		w.PeakAgeMS >= 0 && w.PeakAgeMS <= 2*slotMS
}

// resourceIdentity is the host-owned identity of a serving instance.
type resourceIdentity struct{ AccountID, AppID, DeploymentID string }

// resourceMinutesClient is the apid RPC vmmd sends minutes with.
type resourceMinutesClient interface {
	IngestInstanceResourceMinutes(ctx context.Context, req *apidpb.IngestInstanceResourceMinutesRequest) (*apidpb.IngestInstanceResourceMinutesResponse, error)
}

type resourceMinuteKey struct {
	instance string
	minute   int64
}

// resourceMinutes folds guest slots into wall-clock minutes per instance
// and sends closed minutes to apid. A slot lands in the minute its memory
// peak was sampled in, by vmmd's clock. A minute is sent once; a slot that
// arrives after its minute was sent starts a new row that apid merges.
// Delivery is best-effort: a failed batch is dropped, never resent, so no
// slot is ever counted twice.
type resourceMinutes struct {
	resolve func(instance string) (resourceIdentity, bool)
	client  resourceMinutesClient
	log     *slog.Logger
	now     func() time.Time

	mu       sync.Mutex
	pending  map[resourceMinuteKey]*apidpb.InstanceResourceMinute
	disabled map[string]time.Time // app id → forward again after
	// dropped counts minutes lost to the pending bound and undelivered
	// rows counts rows of batches apid did not take; flush reports both
	// at Warn at most once a minute.
	dropped     int64
	undelivered int64
	lastWarn    time.Time
}

func newResourceMinutes(resolve func(string) (resourceIdentity, bool), client resourceMinutesClient, log *slog.Logger) *resourceMinutes {
	if log == nil {
		log = slog.Default()
	}
	return &resourceMinutes{
		resolve: resolve, client: client, log: log, now: time.Now,
		pending:  map[resourceMinuteKey]*apidpb.InstanceResourceMinute{},
		disabled: map[string]time.Time{},
	}
}

// observe folds one slot received at recv.
func (m *resourceMinutes) observe(instance string, w resourceSlotWire, recv time.Time) {
	if m == nil || !w.valid() {
		return
	}
	id, ok := m.resolve(instance)
	if !ok || id.AppID == "" {
		return
	}
	peakAt := recv.Add(-time.Duration(w.PeakAgeMS) * time.Millisecond)
	k := resourceMinuteKey{instance: instance, minute: peakAt.UTC().Truncate(time.Minute).Unix()}
	m.mu.Lock()
	defer m.mu.Unlock()
	if until, off := m.disabled[id.AppID]; off {
		if recv.Before(until) {
			return
		}
		delete(m.disabled, id.AppID)
	}
	row := m.pending[k]
	if row == nil {
		if len(m.pending) >= api.RouteResourcePendingRowsMax {
			m.dropped++
			return
		}
		row = &apidpb.InstanceResourceMinute{
			AccountId: id.AccountID, AppId: id.AppID, DeploymentId: id.DeploymentID, InstanceId: instance,
			MinuteUnix: k.minute, MemStartMb: int32(w.MemStartMB),
		}
		m.pending[k] = row
	}
	row.Samples += int32(w.Samples)
	row.MemPeakMb = max(row.MemPeakMb, int32(w.MemPeakMB))
	row.MemEndMb = int32(w.MemEndMB)
	row.MemLimitMb = max(row.MemLimitMb, int32(w.MemLimitMB))
	row.CpuMs += w.CPUUsec / 1000
	row.CpuPeakMillicores = max(row.CpuPeakMillicores, int32(w.CPUPeakMillicores))
	row.CpuLimitMillicores = max(row.CpuLimitMillicores, int32(w.CPULimitMillis))
}

// take removes the minutes closed at least RouteResourceMinuteGrace ago,
// or every minute when all is set, oldest first.
func (m *resourceMinutes) take(now time.Time, all bool) []*apidpb.InstanceResourceMinute {
	ready := now.Add(-time.Minute - api.RouteResourceMinuteGrace).Unix()
	m.mu.Lock()
	var out []*apidpb.InstanceResourceMinute
	for k, row := range m.pending {
		if all || k.minute <= ready {
			out = append(out, row)
			delete(m.pending, k)
		}
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].MinuteUnix != out[j].MinuteUnix {
			return out[i].MinuteUnix < out[j].MinuteUnix
		}
		return out[i].InstanceId < out[j].InstanceId
	})
	return out
}

// flush sends the closed minutes in batches.
func (m *resourceMinutes) flush(ctx context.Context, all bool) {
	rows := m.take(m.now(), all)
	for len(rows) > 0 {
		n := min(len(rows), api.RouteResourceBatchRowsMax)
		batch := rows[:n]
		rows = rows[n:]
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		resp, err := m.client.IngestInstanceResourceMinutes(callCtx, &apidpb.IngestInstanceResourceMinutesRequest{Minutes: batch})
		cancel()
		if err != nil {
			m.mu.Lock()
			m.undelivered += int64(n)
			m.mu.Unlock()
			m.log.Debug("resource minutes not delivered; dropped", "rows", n, "err", err)
			continue
		}
		if len(resp.GetDisabledAppIds()) > 0 {
			until := m.now().Add(api.RouteResourceDisabledBackoff)
			m.mu.Lock()
			for _, app := range resp.GetDisabledAppIds() {
				m.disabled[app] = until
			}
			m.mu.Unlock()
		}
	}
	m.warnLosses()
}

// warnLosses reports lost minutes at Warn, at most once a minute, so a
// node whose apid is away for a while shows up without flooding the log.
func (m *resourceMinutes) warnLosses() {
	now := m.now()
	m.mu.Lock()
	dropped, undelivered := m.dropped, m.undelivered
	if (dropped == 0 && undelivered == 0) || now.Sub(m.lastWarn) < time.Minute {
		m.mu.Unlock()
		return
	}
	m.dropped, m.undelivered, m.lastWarn = 0, 0, now
	m.mu.Unlock()
	m.log.Warn("route resource peaks: resource minutes lost",
		"dropped_buffer_full", dropped, "undelivered", undelivered, "pending_bound", api.RouteResourcePendingRowsMax)
}

// run flushes every RouteResourceFlushInterval until ctx ends, then sends
// what is left.
func (m *resourceMinutes) run(ctx context.Context) {
	ticker := time.NewTicker(api.RouteResourceFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			m.flush(final, true)
			cancel()
			return
		case <-ticker.C:
			m.flush(ctx, false)
		}
	}
}

// WithResourceSlots routes type=0x0C resource slots to minutes
// (ADR-20261121170000). Without it the slots are parsed and dropped.
func (r *FrameworkReadyReceiver) WithResourceSlots(m *resourceMinutes) *FrameworkReadyReceiver {
	r.resourceSlots.Store(m)
	return r
}

// startResourceMinutes forwards guest resource slots to apid over the same
// SpansWriter connection target as in-guest traces. Whether an app's minutes
// are kept is apid's decision (the route_resource_peaks platform flag);
// FAAS_ROUTE_RESOURCE_PEAKS=0 stops a node from forwarding at all.
func startResourceMinutes(ctx context.Context, log *slog.Logger, mgr *fcvm.Manager, recv *FrameworkReadyReceiver) {
	if os.Getenv("FAAS_ROUTE_RESOURCE_PEAKS") == "0" || mgr == nil || recv == nil {
		return
	}
	target, tlsCfg, err := traceSpansWriterTarget(os.Getenv)
	if err != nil {
		log.Info("route resource peaks: no apid target; guest resource slots are dropped", "err", err)
		return
	}
	client, err := apidgrpc.DialGuestSpans(ctx, target, tlsCfg)
	if err != nil {
		log.Warn("route resource peaks: dial apid", "err", err)
		return
	}
	minutes := newResourceMinutes(func(instance string) (resourceIdentity, bool) {
		id, err := mgr.InstanceTraceIdentity(instance)
		if err != nil {
			return resourceIdentity{}, false
		}
		return resourceIdentity{AccountID: id.AccountID, AppID: id.AppID, DeploymentID: id.DeploymentID}, true
	}, client, log)
	recv.WithResourceSlots(minutes)
	go func() {
		defer func() { _ = client.Close() }()
		minutes.run(ctx)
	}()
}
