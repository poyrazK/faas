package realtime

import (
	"context"
	"sort"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Realtime usage recording (ADR-1323). Admission, sends and receives
// update per-endpoint atomics; one reporter goroutine samples them every
// UsageSampleInterval into per-hour totals and sends each open or
// undelivered hour to UsageReporter every UsageReportInterval. Nothing here
// bills or limits a customer.

// UsageReporter delivers this process's cumulative usage for one UTC hour.
// Reports for the same incarnation and hour keep the larger values, so the
// reporter may send the same hour any number of times.
type UsageReporter interface {
	ReportRealtimeUsage(context.Context, state.RealtimeUsageReport) error
}

const (
	defaultUsageSampleInterval = time.Second
	usageFinalReportTimeout    = 3 * time.Second
)

// usagePayloadFrames are the v2 frames that carry a customer payload. Control
// frames (subscribed, acknowledgements, presence, errors) are not counted.
var usagePayloadFrames = map[string]bool{"message": true, "direct_message": true, "inbox_message": true, "signal": true}

// usageCounters is one endpoint's activity since the reporter last drained it.
type usageCounters struct {
	peak              atomic.Int64
	deliveredMessages atomic.Int64
	deliveredBytes    atomic.Int64
	receivedMessages  atomic.Int64
	receivedBytes     atomic.Int64
}

func (u *usageCounters) observeConnections(n int64) {
	for {
		current := u.peak.Load()
		if n <= current || u.peak.CompareAndSwap(current, n) {
			return
		}
	}
}

func (u *usageCounters) delivered(bytes int) {
	u.deliveredMessages.Add(1)
	u.deliveredBytes.Add(int64(bytes))
}

func (u *usageCounters) received(bytes int) {
	u.receivedMessages.Add(1)
	u.receivedBytes.Add(int64(bytes))
}

// usageHour accumulates one UTC hour of this process's usage per endpoint.
type usageHour struct {
	hour      time.Time
	endpoints map[string]*usageTotals
}

type usageTotals struct {
	peaks                             [state.RealtimeUsageMinutes]int32
	connectionMillis                  int64
	deliveredMessages, deliveredBytes int64
	receivedMessages, receivedBytes   int64
}

func (t *usageTotals) export(endpointID string) state.RealtimeEndpointUsage {
	return state.RealtimeEndpointUsage{
		EndpointID: endpointID, MinutePeakConnections: append([]int32(nil), t.peaks[:]...),
		ConnectionSeconds: t.connectionMillis / 1000, DeliveredMessages: t.deliveredMessages, DeliveredBytes: t.deliveredBytes,
		ReceivedMessages: t.receivedMessages, ReceivedBytes: t.receivedBytes,
	}
}

type usageRecorder struct {
	m           *Manager
	reporter    UsageReporter
	incarnation string
	now         func() time.Time
	hours       map[time.Time]*usageHour
	lastSample  time.Time
	lastReport  time.Time
	done        chan struct{}
}

func (m *Manager) startUsageRecorder() {
	if m.cfg.UsageReporter == nil {
		return
	}
	now := m.cfg.usageClock
	if now == nil {
		now = time.Now
	}
	r := &usageRecorder{m: m, reporter: m.cfg.UsageReporter, incarnation: uuid.NewString(), now: now,
		hours: make(map[time.Time]*usageHour), done: make(chan struct{})}
	m.usage = r
	go r.run(m.ctx) //nolint:contextcheck // the Manager owns the recorder's lifecycle; Close cancels it.
}

func (r *usageRecorder) run(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(r.m.cfg.UsageSampleInterval)
	defer ticker.Stop()
	r.lastSample = r.now()
	r.lastReport = r.lastSample
	for {
		select {
		case <-ctx.Done():
			// Close cancels the manager context before closing sockets, so the
			// final sample still sees the open connections' last interval.
			r.sample(r.now())
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), usageFinalReportTimeout)
			r.report(final)
			cancel()
			return
		case <-ticker.C:
			now := r.now()
			r.sample(now)
			if now.Sub(r.lastReport) >= r.m.cfg.UsageReportInterval {
				r.lastReport = now
				reportCtx, cancel := context.WithTimeout(ctx, r.m.cfg.UsageReportInterval)
				r.report(reportCtx)
				cancel()
			}
		}
	}
}

// sample drains every endpoint's counters into the current hour and adds the
// connections held since the previous sample.
func (r *usageRecorder) sample(now time.Time) {
	elapsed := now.Sub(r.lastSample).Milliseconds()
	r.lastSample = now
	if elapsed < 0 {
		elapsed = 0
	}
	hourStart := now.UTC().Truncate(time.Hour)
	minute := now.UTC().Minute()
	r.m.endpoints.Range(func(key, value any) bool {
		endpoint := value.(*endpointState)
		open := endpoint.reserved.Load()
		if open < 0 {
			open = 0
		}
		peak := max(endpoint.usage.peak.Swap(open), open)
		delivered, deliveredBytes := endpoint.usage.deliveredMessages.Swap(0), endpoint.usage.deliveredBytes.Swap(0)
		received, receivedBytes := endpoint.usage.receivedMessages.Swap(0), endpoint.usage.receivedBytes.Swap(0)
		if peak == 0 && delivered == 0 && received == 0 {
			return true
		}
		hour := r.hours[hourStart]
		if hour == nil {
			hour = &usageHour{hour: hourStart, endpoints: make(map[string]*usageTotals)}
			r.hours[hourStart] = hour
		}
		totals := hour.endpoints[key.(string)]
		if totals == nil {
			totals = &usageTotals{}
			hour.endpoints[key.(string)] = totals
		}
		// peak is bounded by the endpoint's connection limit, far below int32.
		totals.peaks[minute] = max(totals.peaks[minute], int32(peak))
		totals.connectionMillis += open * elapsed
		totals.deliveredMessages += delivered
		totals.deliveredBytes += deliveredBytes
		totals.receivedMessages += received
		totals.receivedBytes += receivedBytes
		return true
	})
}

// report sends every held hour, oldest first. A completed hour is dropped once
// delivered or once it is too old for apid to accept; the current hour stays,
// because later samples keep adding to it.
func (r *usageRecorder) report(ctx context.Context) {
	current := r.now().UTC().Truncate(time.Hour)
	hours := make([]time.Time, 0, len(r.hours))
	for h := range r.hours {
		hours = append(hours, h)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i].Before(hours[j]) })
	for _, h := range hours {
		if current.Sub(h) > api.RealtimeUsageReportMaxAge {
			delete(r.hours, h)
			r.m.usageDroppedHours.Add(1)
			continue
		}
		if err := r.send(ctx, r.hours[h]); err != nil {
			r.m.usageHeldHours.Store(int64(len(r.hours)))
			r.m.usageReportFailures.Add(1)
			if r.m.cfg.UsageReportFailure != nil {
				r.m.cfg.UsageReportFailure(err)
			}
			return
		}
		r.m.usageReports.Add(1)
		if h.Before(current) {
			delete(r.hours, h)
		}
	}
	r.m.usageHeldHours.Store(int64(len(r.hours)))
}

func (r *usageRecorder) send(ctx context.Context, hour *usageHour) error {
	ids := make([]string, 0, len(hour.endpoints))
	for id := range hour.endpoints {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for start := 0; start < len(ids); start += api.RealtimeUsageReportMaxEndpoints {
		chunk := ids[start:min(start+api.RealtimeUsageReportMaxEndpoints, len(ids))]
		report := state.RealtimeUsageReport{Incarnation: r.incarnation, Hour: hour.hour, Endpoints: make([]state.RealtimeEndpointUsage, 0, len(chunk))}
		for _, id := range chunk {
			report.Endpoints = append(report.Endpoints, hour.endpoints[id].export(id))
		}
		if err := r.reporter.ReportRealtimeUsage(ctx, report); err != nil {
			return err
		}
	}
	return nil
}
