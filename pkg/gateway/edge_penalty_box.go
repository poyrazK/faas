package gateway

// Penalty box (ADR-20261022105846). A rule with a penalty counts the
// clients it refuses by policy; once one reaches the rule's threshold
// within its window, every request from that client to the app is refused
// until the penalty ends. Counts live in this gateway's memory. Boxes do
// too, and with a sharer (amendment 1) each box this gateway makes is
// reported through OnBox and boxes other gateways made arrive through Hold,
// so a box holds on every node and survives a restart.

import (
	"context"
	"math"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// PenaltyEdgeRuleMatcher is optional so matchers without penalty support
// keep compiling. RulePenalty returns the enforced penalty of rule ruleID as
// it applies to host, or false when the rule does not apply there, is in log
// mode, or has no penalty.
type PenaltyEdgeRuleMatcher interface {
	RulePenalty(ctx context.Context, host, ruleID string) (api.EdgeRulePenalty, bool)
}

type penaltyHitKey struct {
	app, rule string
	client    netip.Prefix
}

type penaltyHits struct {
	count int
	since time.Time
}

type penaltyEntry struct {
	ruleID string
	until  time.Time
}

// PenaltyBoxRecord is one box as gateways share it (amendment 1).
type PenaltyBoxRecord struct {
	AppID, RuleID string
	Client        netip.Prefix
	Until         time.Time
}

// PenaltyBox is one gateway's boxes and pending rejection counts. It is safe
// for concurrent use; the request path takes its lock only while it holds
// at least one box.
type PenaltyBox struct {
	now   func() time.Time
	count atomic.Int64 // boxes held, for the lock-free fast path
	// onBox, when set, is told of every box a Hit makes; it must not block.
	onBox func(PenaltyBoxRecord)

	mu    sync.Mutex
	boxes map[string]map[netip.Prefix]penaltyEntry // app -> client -> box
	hits  map[penaltyHitKey]penaltyHits
}

// NewPenaltyBox returns an empty box.
func NewPenaltyBox() *PenaltyBox {
	return &PenaltyBox{
		now:   time.Now,
		boxes: map[string]map[netip.Prefix]penaltyEntry{},
		hits:  map[penaltyHitKey]penaltyHits{},
	}
}

// penaltyClient is the identity a box holds: an IPv4 address, or an IPv6
// client's /64, which one host can rotate within.
func penaltyClient(ip net.IP) (netip.Prefix, bool) {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Prefix{}, false
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return netip.PrefixFrom(addr, 32), true
	}
	return netip.PrefixFrom(addr, 64).Masked(), true
}

// Hit records one policy rejection of ip by rule for app and reports
// whether it boxed the client. Counting fails open: with the tables full,
// the rejection still happens but is not counted.
func (b *PenaltyBox) Hit(app, rule string, ip net.IP, p api.EdgeRulePenalty) bool {
	client, ok := penaltyClient(ip)
	if b == nil || !ok || p.Seconds <= 0 {
		return false
	}
	boxed, until := b.hit(app, rule, client, p)
	if boxed && b.onBox != nil {
		b.onBox(PenaltyBoxRecord{AppID: app, RuleID: rule, Client: client, Until: until})
	}
	return boxed
}

// hit counts one rejection under the lock and reports whether it boxed the
// client, and until when.
func (b *PenaltyBox) hit(app, rule string, client netip.Prefix, p api.EdgeRulePenalty) (bool, time.Time) {
	now := b.now()
	b.mu.Lock()
	defer b.mu.Unlock()
	key := penaltyHitKey{app: app, rule: rule, client: client}
	h, seen := b.hits[key]
	if !seen || now.Sub(h.since) >= time.Duration(p.WithinSeconds)*time.Second {
		h = penaltyHits{since: now}
	}
	h.count++
	if h.count < max(p.After, 1) {
		if !seen && len(b.hits) >= api.EdgePenaltyBoxMaxEntries && !b.sweepHitsLocked(now) {
			return false, time.Time{}
		}
		b.hits[key] = h
		return false, time.Time{}
	}
	delete(b.hits, key)
	until := now.Add(time.Duration(p.Seconds) * time.Second)
	return b.boxLocked(app, client, penaltyEntry{ruleID: rule, until: until}, now), until
}

// OnBox reports every box a Hit makes to fn, which must not block. Set it
// before the box is used.
func (b *PenaltyBox) OnBox(fn func(PenaltyBoxRecord)) {
	if b != nil {
		b.onBox = fn
	}
}

// Hold applies a box another gateway made, or one this gateway made before
// a restart. Like a box made here, it keeps the later end of one already
// held, and it is bounded by the same per-app and per-gateway caps. An
// ended box or a client that is not a box identity is ignored.
func (b *PenaltyBox) Hold(rec PenaltyBoxRecord) bool {
	if b == nil || !rec.Client.IsValid() || rec.Client != penaltyIdentity(rec.Client) {
		return false
	}
	now := b.now()
	if !now.Before(rec.Until) {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.boxLocked(rec.AppID, rec.Client, penaltyEntry{ruleID: rec.RuleID, until: rec.Until}, now)
}

// penaltyIdentity is the box identity of a prefix's address: what
// penaltyClient returns for it.
func penaltyIdentity(p netip.Prefix) netip.Prefix {
	addr := p.Addr().Unmap()
	if addr.Is4() {
		return netip.PrefixFrom(addr, 32)
	}
	return netip.PrefixFrom(addr, 64).Masked()
}

// boxLocked holds client for app. A client already held keeps the later
// end. A full app drops its soonest-ending box; a full gateway that frees
// nothing by expiry refuses the box.
func (b *PenaltyBox) boxLocked(app string, client netip.Prefix, entry penaltyEntry, now time.Time) bool {
	if held := b.boxes[app]; held != nil {
		if prev, ok := held[client]; ok {
			if prev.until.After(entry.until) {
				entry = prev
			}
			held[client] = entry
			return true
		}
		if len(held) >= api.EdgePenaltyBoxMaxPerApp {
			b.dropSoonestLocked(held)
			held[client] = entry
			b.count.Add(1)
			return true
		}
	}
	if b.count.Load() >= api.EdgePenaltyBoxMaxEntries && !b.sweepBoxesLocked(now) {
		return false
	}
	// Fetched again: the sweep may have dropped the app's emptied map.
	held := b.boxes[app]
	if held == nil {
		held = map[netip.Prefix]penaltyEntry{}
		b.boxes[app] = held
	}
	held[client] = entry
	b.count.Add(1)
	return true
}

func (b *PenaltyBox) dropSoonestLocked(held map[netip.Prefix]penaltyEntry) {
	var soonest netip.Prefix
	var until time.Time
	for client, e := range held {
		if until.IsZero() || e.until.Before(until) {
			soonest, until = client, e.until
		}
	}
	delete(held, soonest)
	b.count.Add(-1)
}

// sweepBoxesLocked drops ended boxes and reports whether any were dropped.
func (b *PenaltyBox) sweepBoxesLocked(now time.Time) bool {
	freed := false
	for app, held := range b.boxes {
		for client, e := range held {
			if !now.Before(e.until) {
				delete(held, client)
				b.count.Add(-1)
				freed = true
			}
		}
		if len(held) == 0 {
			delete(b.boxes, app)
		}
	}
	return freed
}

// sweepHitsLocked drops counts older than the longest window and reports
// whether any were dropped.
func (b *PenaltyBox) sweepHitsLocked(now time.Time) bool {
	freed := false
	for key, h := range b.hits {
		if now.Sub(h.since) >= api.MaxEdgePenaltyWithinSeconds*time.Second {
			delete(b.hits, key)
			freed = true
		}
	}
	return freed
}

// Boxed returns the rule holding ip for app and how long the box has left.
func (b *PenaltyBox) Boxed(app string, ip net.IP) (ruleID string, left time.Duration, ok bool) {
	if b == nil || b.count.Load() == 0 {
		return "", 0, false
	}
	client, ok := penaltyClient(ip)
	if !ok {
		return "", 0, false
	}
	now := b.now()
	b.mu.Lock()
	defer b.mu.Unlock()
	held := b.boxes[app]
	e, ok := held[client]
	if !ok {
		return "", 0, false
	}
	if !now.Before(e.until) {
		delete(held, client)
		b.count.Add(-1)
		if len(held) == 0 {
			delete(b.boxes, app)
		}
		return "", 0, false
	}
	return e.ruleID, e.until.Sub(now), true
}

// WithPenaltyBox arms rule penalties (ADR-20261022105846). nil leaves
// penalties unenforced.
func (h *Handler) WithPenaltyBox(b *PenaltyBox) *Handler {
	h.penaltyBox = b
	return h
}

// edgeRulePenalty is ruleID's enforced penalty where this request
// arrived, if any.
func (h *Handler) edgeRulePenalty(r *http.Request, ruleID string) (api.EdgeRulePenalty, bool) {
	m, ok := h.edgeRules.(PenaltyEdgeRuleMatcher)
	if !ok || h.penaltyBox == nil {
		return api.EdgeRulePenalty{}, false
	}
	return m.RulePenalty(r.Context(), hostname(r.Host), ruleID)
}

// applyEdgePenaltyBox refuses a client a penalty box holds, before any
// other edge gate. The box applies only where its rule still applies with
// a penalty, so removing the rule or its penalty, disabling it, or putting
// it in log mode releases its clients, and so does the edge_penalty_box
// platform flag leaving the account out (ADR-20261023113721). Deployment
// smoke checks are never refused.
func (h *Handler) applyEdgePenaltyBox(w http.ResponseWriter, r *http.Request, app App, deploymentSmoke bool) bool {
	if h.penaltyBox == nil || deploymentSmoke || !h.edgeFeatureReleased(platformflags.EdgePenaltyBox, app) {
		return false
	}
	ruleID, left, ok := h.penaltyBox.Boxed(app.ID, challengeClientIP(r))
	if !ok {
		return false
	}
	if _, still := h.edgeRulePenalty(r, ruleID); !still {
		return false
	}
	api.WriteProblem(w, api.ErrPenaltyBox(ruleID, int(math.Ceil(left.Seconds()))))
	h.metrics.ObserveEdgePenalty(app.ID, "rejected")
	return true
}

// penalizeEdgeRuleRejection counts one policy rejection by ruleID toward
// its penalty box. Callers are the policy decisions only (an IP or country
// refused, a throttle exhausted, a WAF block), never a gate's own fault
// such as a forged forwarding header or a missing GeoIP database.
func (h *Handler) penalizeEdgeRuleRejection(r *http.Request, app App, ruleID string) {
	p, ok := h.edgeRulePenalty(r, ruleID)
	if !ok || !h.edgeFeatureReleased(platformflags.EdgePenaltyBox, app) {
		return
	}
	if h.penaltyBox.Hit(app.ID, ruleID, challengeClientIP(r), p) {
		h.metrics.ObserveEdgePenalty(app.ID, "boxed")
		h.recordPenaltyBoxEvent(r, ruleID, app.ID)
	}
}

// EdgeRuleEventBoxed is the event outcome for a client put in a penalty box
// (ADR-20261022105846 amendment 2), recorded from the request whose
// rejection tipped it in. Only the gateway that boxed the client records it;
// the gateways the box is shared to do not.
const EdgeRuleEventBoxed = "boxed"

// PenaltyEventRecorder samples penalty box events on a per-rule budget of
// their own, so a rule's WAF detections or challenge outcomes cannot crowd
// its boxes out, nor boxes them.
type PenaltyEventRecorder interface {
	SampleEdgePenaltyBox(ruleID string) bool
	RecordEdgeRuleEvent(ev EdgeRuleEvent)
}

// recordPenaltyBoxEvent records that the rule boxed the request's client,
// so the box can be reviewed after its shared row is pruned.
func (h *Handler) recordPenaltyBoxEvent(r *http.Request, ruleID, appID string) {
	rec, ok := h.edgeRuleHits.(PenaltyEventRecorder)
	if !ok || !rec.SampleEdgePenaltyBox(ruleID) {
		return
	}
	rec.RecordEdgeRuleEvent(outcomeEvent(r, ruleID, appID, EdgeRuleEventBoxed))
}
