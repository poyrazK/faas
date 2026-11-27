package gateway

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// InstanceRouteMinute is one route's activity on one instance in one UTC
// minute (ADR-20261121170000). A request in flight across several minutes
// is present in each of them; it counts as a request only in the minute it
// finished. BusyMS is the in-flight time within the minute, summed over
// concurrent requests.
type InstanceRouteMinute struct {
	AccountID    uuid.UUID
	AppID        uuid.UUID
	InstanceID   string
	Minute       time.Time
	Route        string
	Method       string
	Requests     int64
	BusyMS       int64
	MaxLatencyMS int
}

// InstanceRouteShipFn sends one batch to apid. It is called once per batch;
// a failed batch is dropped, so a lost answer never counts twice.
type InstanceRouteShipFn func(ctx context.Context, rows []InstanceRouteMinute) error

// InstanceRouteMinutesConfig wires the per-instance route counter.
type InstanceRouteMinutesConfig struct {
	Ship InstanceRouteShipFn
	// OnDropped receives rows that did not fit or did not ship, for metrics.
	OnDropped func(int)
	Now       func() time.Time
}

// instanceRouteSpanMax bounds the minutes one request is present in, so a
// long-lived stream marks its last minutes, not an hour of them.
const instanceRouteSpanMax = 15

type instanceRouteKey struct {
	app      uuid.UUID
	instance string
	minute   int64
	route    string
	method   string
}

type instanceMinuteKey struct {
	app      uuid.UUID
	instance string
	minute   int64
}

type instanceRouteMinutes struct {
	cfg InstanceRouteMinutesConfig
	log *slog.Logger

	mu      sync.Mutex
	pending map[instanceRouteKey]*InstanceRouteMinute
	routes  map[instanceMinuteKey]int
}

func newInstanceRouteMinutes(cfg InstanceRouteMinutesConfig, log *slog.Logger) *instanceRouteMinutes {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &instanceRouteMinutes{cfg: cfg, log: log,
		pending: map[instanceRouteKey]*InstanceRouteMinute{}, routes: map[instanceMinuteKey]int{}}
}

// add counts one finished request.
func (c *instanceRouteMinutes) add(account, app uuid.UUID, instance, route, method string, end time.Time, latency time.Duration) {
	if c == nil || instance == "" || app == uuid.Nil || end.IsZero() {
		return
	}
	latency = max(latency, 0)
	end = end.UTC()
	start := end.Add(-latency)
	last := end.Truncate(time.Minute)
	first := start.Truncate(time.Minute)
	if floor := last.Add(-(instanceRouteSpanMax - 1) * time.Minute); first.Before(floor) {
		first = floor
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	dropped := 0
	for m := first; !m.After(last); m = m.Add(time.Minute) {
		from, to := m, m.Add(time.Minute)
		if start.After(from) {
			from = start
		}
		if end.Before(to) {
			to = end
		}
		busy := to.Sub(from)
		row := c.rowLocked(account, app, instance, route, method, m)
		if row == nil {
			dropped++
			continue
		}
		row.BusyMS += max(busy.Milliseconds(), 0)
		if m.Equal(last) {
			row.Requests++
			row.MaxLatencyMS = max(row.MaxLatencyMS, int(latency.Milliseconds()))
		}
	}
	if dropped > 0 && c.cfg.OnDropped != nil {
		c.cfg.OnDropped(dropped)
	}
}

func (c *instanceRouteMinutes) rowLocked(account, app uuid.UUID, instance, route, method string, minute time.Time) *InstanceRouteMinute {
	mk := instanceMinuteKey{app: app, instance: instance, minute: minute.Unix()}
	k := instanceRouteKey{app: app, instance: instance, minute: mk.minute, route: route, method: method}
	if row := c.pending[k]; row != nil {
		return row
	}
	if c.routes[mk] >= api.RouteResourceRoutesPerInstanceMinuteMax {
		k.route, k.method = api.RouteResourceOtherRoute, ""
		route, method = k.route, k.method
		if row := c.pending[k]; row != nil {
			return row
		}
	}
	if len(c.pending) >= api.RouteResourcePendingRowsMax {
		return nil
	}
	row := &InstanceRouteMinute{AccountID: account, AppID: app, InstanceID: instance, Minute: minute, Route: route, Method: method}
	c.pending[k] = row
	c.routes[mk]++
	return row
}

// take removes the minutes closed at least RouteResourceMinuteGrace ago,
// or every minute when all is set.
func (c *instanceRouteMinutes) take(now time.Time, all bool) []InstanceRouteMinute {
	ready := now.Add(-time.Minute - api.RouteResourceMinuteGrace).Unix()
	c.mu.Lock()
	var out []InstanceRouteMinute
	for k, row := range c.pending {
		if all || k.minute <= ready {
			out = append(out, *row)
			delete(c.pending, k)
		}
	}
	for mk := range c.routes {
		if all || mk.minute <= ready {
			delete(c.routes, mk)
		}
	}
	c.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.Minute.Equal(b.Minute) {
			return a.Minute.Before(b.Minute)
		}
		if a.InstanceID != b.InstanceID {
			return a.InstanceID < b.InstanceID
		}
		if a.Route != b.Route {
			return a.Route < b.Route
		}
		return a.Method < b.Method
	})
	return out
}

func (c *instanceRouteMinutes) flush(ctx context.Context, all bool) {
	rows := c.take(c.cfg.Now(), all)
	for len(rows) > 0 {
		n := min(len(rows), api.RouteResourceBatchRowsMax)
		batch := rows[:n]
		rows = rows[n:]
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := c.cfg.Ship(callCtx, batch)
		cancel()
		if err != nil {
			c.log.Debug("instance route minutes not delivered; dropped", "rows", n, "err", err)
			if c.cfg.OnDropped != nil {
				c.cfg.OnDropped(n)
			}
		}
	}
}

// run flushes every RouteResourceFlushInterval until ctx ends, then sends
// what is left.
func (c *instanceRouteMinutes) run(ctx context.Context) {
	ticker := time.NewTicker(api.RouteResourceFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			c.flush(final, true)
			cancel()
			return
		case <-ticker.C:
			c.flush(ctx, false)
		}
	}
}

// WithInstanceRouteMinutes makes the handler count each instance's route
// activity per minute for apps the route_resource_peaks flag releases, and
// starts the shipper on ctx. Without platform flags nothing is counted: the
// feature is dark by default.
func (h *Handler) WithInstanceRouteMinutes(ctx context.Context, cfg InstanceRouteMinutesConfig) *Handler {
	if cfg.Ship == nil {
		return h
	}
	c := newInstanceRouteMinutes(cfg, h.log)
	h.instanceRoutes = c
	go c.run(ctx)
	return h
}

// countInstanceRoute counts one finished request when the app is released.
func (h *Handler) countInstanceRoute(account, app uuid.UUID, plan, instance, route, method string, end time.Time, latency time.Duration) {
	if h.instanceRoutes == nil || h.edgeFeatureFlags == nil || instance == "" {
		return
	}
	if !h.edgeFeatureFlags.Enabled(platformflags.RouteResourcePeaks, platformflags.Subject{AccountID: account.String(), Plan: plan}) {
		return
	}
	h.instanceRoutes.add(account, app, instance, route, method, end, latency)
}
