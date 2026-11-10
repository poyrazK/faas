package clientlimit

// adr: 20261110123517 — a compute edge sheds load before its node saturates.
//
// The per-address limits bound one client; nothing else bounds the node. An
// Overload follows the node's CPU signals into a level from 0 to 1, and the
// edge sheds by it: new connections before TLS first, then requests for
// unknown hosts, then each address beyond a shrinking share, then everything.
// An address that keeps sending once refused is cut off.

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Reasons the overload level refuses a connection or request, on the same
// counter as the client limits' reasons.
const (
	// ReasonOverloadConnection resets a new connection before TLS.
	ReasonOverloadConnection = "overload_connection"
	// ReasonOverloadUnknownHost refuses a request for a host that has not
	// served an app's answer.
	ReasonOverloadUnknownHost = "overload_unknown_host"
	// ReasonOverloadAddress refuses a request beyond its address's share.
	ReasonOverloadAddress = "overload_address"
	// ReasonOverloadRequest refuses any request past the level's midpoint.
	ReasonOverloadRequest = "overload_request"
	// ReasonOverloadCooloff refuses a cut-off address, before TLS or on a
	// connection it still holds.
	ReasonOverloadCooloff = "overload_cooloff"
)

// OverloadReasons lists the overload refusal reasons, so a counter can start
// each at zero.
var OverloadReasons = []string{ReasonOverloadConnection, ReasonOverloadUnknownHost, ReasonOverloadAddress, ReasonOverloadRequest, ReasonOverloadCooloff}

// OverloadMode is FAAS_EDGE_OVERLOAD: off, observe (measure and export,
// refuse nothing) or on.
type OverloadMode int

const (
	OverloadOff OverloadMode = iota
	OverloadObserve
	OverloadOn
)

// ParseOverloadMode reads a mode; empty is off.
func ParseOverloadMode(v string) (OverloadMode, error) {
	switch strings.TrimSpace(v) {
	case "", "off":
		return OverloadOff, nil
	case "observe":
		return OverloadObserve, nil
	case "on":
		return OverloadOn, nil
	default:
		return OverloadOff, fmt.Errorf("overload mode %q, want off, observe or on", v)
	}
}

func (m OverloadMode) String() string {
	switch m {
	case OverloadObserve:
		return "observe"
	case OverloadOn:
		return "on"
	default:
		return "off"
	}
}

// OverloadSignals is one reading of the signals, each over the last window.
type OverloadSignals struct {
	// SchedDelayP99 is the 99th percentile of the time the edge's goroutines
	// waited to run.
	SchedDelayP99 time.Duration
	// NodeCPUPressure is the share of time any task on the node waited for a
	// CPU, from 0 to 1.
	NodeCPUPressure float64
	// GatewayCPUShare is the gateway daemons' CPU use as a share of the
	// node's CPUs, from 0 to 1.
	GatewayCPUShare float64
}

// OverloadThresholds decide when the edge is overloaded and how fast the
// level moves.
type OverloadThresholds struct {
	SchedDelayP99   time.Duration
	NodeCPUPressure float64
	GatewayCPUShare float64
	// Step raises the level on an overloaded tick, RecoveryStep lowers it on
	// a calm one: every signal below CalmFraction of its threshold.
	Step, RecoveryStep, CalmFraction float64
}

// DefaultOverloadThresholds are the values in pkg/api/limits.go.
func DefaultOverloadThresholds() OverloadThresholds {
	return OverloadThresholds{
		SchedDelayP99:   api.EdgeOverloadSchedDelayP99,
		NodeCPUPressure: api.EdgeOverloadNodeCPUPressure,
		GatewayCPUShare: api.EdgeOverloadGatewayCPUShare,
		Step:            api.EdgeOverloadLevelStep,
		RecoveryStep:    api.EdgeOverloadLevelRecoveryStep,
		CalmFraction:    api.EdgeOverloadCalmFraction,
	}
}

// exceeded reports whether s passes the thresholds scaled by f. Node CPU
// pressure counts only while the gateways use their share of the node, so a
// CPU-hungry tenant does not make the edge refuse traffic that is not what
// fills the node.
func (t OverloadThresholds) exceeded(s OverloadSignals, f float64) bool {
	return float64(s.SchedDelayP99) > f*float64(t.SchedDelayP99) ||
		s.NodeCPUPressure > f*t.NodeCPUPressure && s.GatewayCPUShare > f*t.GatewayCPUShare
}

// OverloadConfig builds an Overload.
type OverloadConfig struct {
	Mode       OverloadMode
	Thresholds OverloadThresholds
	// CPUs sizes the node-wide floor of handshakes admitted while shedding.
	CPUs int
	// Now and Rand default to the clock and math/rand; tests set them.
	Now  func() time.Time
	Rand func() float64
}

// Overload holds a compute edge's overload level and the state it sheds by:
// the floor of handshakes always admitted and the addresses cut off. A nil
// *Overload sheds nothing.
type Overload struct {
	mode  OverloadMode
	th    OverloadThresholds
	level atomic.Uint64 // math.Float64bits of the level
	now   func() time.Time
	rand  func() float64
	floor *bucket
	cool  *cooloff
}

// NewOverload returns an Overload at level 0.
func NewOverload(cfg OverloadConfig) *Overload {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Float64
	}
	floorRate := float64(api.EdgeOverloadHandshakesFloorPerCPU * max(cfg.CPUs, 1))
	return &Overload{
		mode:  cfg.Mode,
		th:    cfg.Thresholds,
		now:   cfg.Now,
		rand:  cfg.Rand,
		floor: &bucket{rate: floorRate, burst: floorRate, tokens: floorRate, last: cfg.Now()},
		cool: &cooloff{
			limit: api.EdgeOverloadRefusalsPerAddrMax,
			d:     api.EdgeOverloadCooloff,
			max:   api.EdgeClientTrackedAddrsMax,
			addrs: map[netip.Prefix]*cooloffEntry{},
		},
	}
}

// Mode is the mode the Overload was built with.
func (o *Overload) Mode() OverloadMode {
	if o == nil {
		return OverloadOff
	}
	return o.mode
}

// Level is the measured level, from 0 to 1, in every mode.
func (o *Overload) Level() float64 {
	if o == nil {
		return 0
	}
	return math.Float64frombits(o.level.Load())
}

// Shedding is the level the edge acts on: Level while on, 0 otherwise.
func (o *Overload) Shedding() float64 {
	if o.Mode() != OverloadOn {
		return 0
	}
	return o.Level()
}

// Update moves the level by one tick's signals and returns it.
func (o *Overload) Update(s OverloadSignals) float64 {
	l := o.Level()
	switch {
	case o.th.exceeded(s, 1):
		l = min(1, l+o.th.Step)
	case !o.th.exceeded(s, o.th.CalmFraction):
		l = max(0, l-o.th.RecoveryStep)
	}
	o.level.Store(math.Float64bits(l))
	return l
}

// Run reads sample every tick and updates the level until ctx ends. observe,
// when set, sees each tick's signals and level; sampleErr sees the sampler's
// first error (a signal it cannot read counts as zero).
func (o *Overload) Run(ctx context.Context, sample func(time.Time) (OverloadSignals, error), tick time.Duration, observe func(OverloadSignals, float64), sampleErr func(error)) {
	t := time.NewTicker(tick)
	defer t.Stop()
	reported := false
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s, err := sample(now)
			if err != nil && !reported && sampleErr != nil {
				reported = true
				sampleErr(err)
			}
			l := o.Update(s)
			if observe != nil {
				observe(s, l)
			}
		}
	}
}

// ShedConnection reports whether a new connection from key, before its TLS
// handshake, is reset, and why: its address is cut off, or, with the level's
// probability, the node-wide floor of handshakes is spent. A proxy's
// connection carries many clients, which are shed per request instead.
func (o *Overload) ShedConnection(key netip.Prefix, proxy bool) (string, bool) {
	l := o.Shedding()
	if l == 0 {
		return "", false
	}
	now := o.now()
	if key.IsValid() && o.cool.active(key, now) {
		return ReasonOverloadCooloff, true
	}
	if proxy || o.rand() >= l || o.floor.take(now) {
		return "", false
	}
	return ReasonOverloadConnection, true
}

// Shed refuses requests while the edge is overloaded and on (ADR-20261110123517
// §3–4), each with a 503 edge_overloaded and a jittered Retry-After on a
// connection that stays open, in this order: a cut-off address; a host known
// does not hold, with probability min(1, 2L); a request beyond its address's
// share, the per-address rate scaled by 1−L down to a floor; and, past level
// 0.5, any request with probability 2L−1. An address refused more than its
// limit in one second is cut off: its refusals carry Connection: close, which
// net/http turns into a GOAWAY on HTTP/2, and its new connections are reset
// before TLS. clientAddr names the request's client and host its hostname;
// a request with neither passes the steps that need them. A nil known counts
// every host as known.
func Shed(next http.Handler, o *Overload, clientAddr func(*http.Request) (netip.Addr, bool), host func(*http.Request) (string, bool), known *KnownHosts, refused func(reason string)) http.Handler {
	if o.Mode() != OverloadOn {
		return next
	}
	perAddr := newTable[netip.Prefix](api.EdgeClientRequestsPerSecondPerAddr, api.EdgeClientRequestsPerSecondPerAddr, api.EdgeClientTrackedAddrsMax, o.now)
	floorScale := float64(api.EdgeOverloadRequestsPerSecondPerAddrFloor) / float64(api.EdgeClientRequestsPerSecondPerAddr)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := o.Shedding()
		if l == 0 {
			next.ServeHTTP(w, r)
			return
		}
		var key netip.Prefix
		if addr, ok := clientAddr(r); ok {
			key = Key(addr)
		}
		now := o.now()
		reason := ""
		switch {
		case key.IsValid() && o.cool.active(key, now):
			reason = ReasonOverloadCooloff
		case !isKnownHost(r, host, known) && o.rand() < min(1, 2*l):
			reason = ReasonOverloadUnknownHost
		case key.IsValid() && !perAddr.takeN(key, 1/max(floorScale, 1-l)):
			reason = ReasonOverloadAddress
		case l > 0.5 && o.rand() < 2*l-1:
			reason = ReasonOverloadRequest
		default:
			next.ServeHTTP(w, r)
			return
		}
		refused(reason)
		cutOff := reason == ReasonOverloadCooloff || key.IsValid() && o.cool.refused(key, now)
		o.writeOverloaded(w, cutOff)
	})
}

// isKnownHost reports whether r's host has served an app's answer.
func isKnownHost(r *http.Request, host func(*http.Request) (string, bool), known *KnownHosts) bool {
	if known == nil {
		return true
	}
	name, ok := host(r)
	return ok && known.has(name)
}

// writeOverloaded answers 503 edge_overloaded. cutOff asks the server to
// close the connection once its open streams finish.
func (o *Overload) writeOverloaded(w http.ResponseWriter, cutOff bool) {
	span := api.EdgeOverloadRetryAfterMax - api.EdgeOverloadRetryAfterMin + 1
	retry := api.EdgeOverloadRetryAfterMin + min(int(o.rand()*float64(span)), span-1)
	if cutOff {
		w.Header().Set("Connection", "close")
	}
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "edge_overloaded",
		"Edge overloaded", "this node is shedding load; retry after the Retry-After delay").
		WithDocs(wire.DocsBaseURL+"/errors"))
}

// bucket is one token bucket.
type bucket struct {
	mu                  sync.Mutex
	rate, burst, tokens float64
	last                time.Time
}

func (b *bucket) take(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(b.burst, b.tokens+elapsed*b.rate)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// cooloff counts each address's overload refusals in one-second windows and
// cuts off one that passes limit for d. At most max addresses are tracked;
// past that a new address is not counted, so memory stays flat.
type cooloff struct {
	mu        sync.Mutex
	limit     int
	d         time.Duration
	max       int
	addrs     map[netip.Prefix]*cooloffEntry
	lastSweep time.Time
}

type cooloffEntry struct {
	start time.Time // the current window's start
	n     int       // refusals in it
	until time.Time // cut off until
}

// active reports whether key is cut off at now.
func (c *cooloff) active(key netip.Prefix, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.addrs[key]
	return e != nil && now.Before(e.until)
}

// refused counts one refusal of key and reports whether key is cut off.
func (c *cooloff) refused(key netip.Prefix, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Sub(c.lastSweep) >= sweepInterval {
		c.sweep(now)
	}
	e := c.addrs[key]
	if e == nil {
		if len(c.addrs) >= c.max {
			c.sweep(now)
			if len(c.addrs) >= c.max {
				return false
			}
		}
		e = &cooloffEntry{start: now}
		c.addrs[key] = e
	}
	if now.Before(e.until) {
		return true
	}
	if now.Sub(e.start) >= time.Second {
		e.start, e.n = now, 0
	}
	e.n++
	if e.n > c.limit {
		e.until = now.Add(c.d)
		return true
	}
	return false
}

// sweep forgets addresses neither cut off nor refused in the last second.
// The caller holds c.mu.
func (c *cooloff) sweep(now time.Time) {
	for key, e := range c.addrs {
		if !now.Before(e.until) && now.Sub(e.start) >= time.Second {
			delete(c.addrs, key)
		}
	}
	c.lastSweep = now
}
