package gateway

import (
	"errors"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

// requestBodyBudget bounds the request-body bytes one gateway holds at once.
// Without it a handful of concurrent large uploads to any public app could
// fill the spool volume or, for bodies held in the heap, push the daemon past
// its memory cap and take every app on the node down with it.
//
// One owner (an app's resource owner) holds at most ownerMax of the bytes, so
// uploads to one customer's apps, legitimate or not, cannot spend the node's
// whole budget and refuse every other tenant's large requests (H94).
type requestBodyBudget struct {
	maxBytes     int64
	ownerMax     int64 // 0 = no per-owner cap
	perClientMax int   // 0 = no per-client cap

	mu        sync.Mutex
	used      int64
	perOwner  map[string]int64
	perClient map[string]int
}

// newRequestBodyMemoryBudget is the node's heap budget for validate-rule
// bodies, with its per-owner and per-client shares (ADR-091 amendment
// 2026-11-04, round 12).
func newRequestBodyMemoryBudget() *requestBodyBudget {
	b := newRequestBodyBudget(api.RequestBodyMemoryNodeBytes, api.RequestBodyMemoryPerClient)
	b.ownerMax = api.RequestBodyMemoryNodeBytes / api.RequestBodyMemoryOwnerShareDivisor
	return b
}

// errRequestBodyBudget is a body read the budget refused to hold.
var errRequestBodyBudget = errors.New("gateway: request body budget spent")

// budgetedBodyReader grows its lease as body bytes arrive, a step at a
// time, and fails the read once the budget refuses.
type budgetedBodyReader struct {
	r     io.Reader
	lease *requestBodyLease
	read  int64
}

func (b *budgetedBodyReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.read += int64(n)
	if over := b.read - b.lease.bytes; over > 0 && b.lease.budget != nil {
		if !b.lease.grow(max(over, api.RequestBodyMemoryReserveStep)) {
			return n, errRequestBodyBudget
		}
	}
	return n, err
}

func newRequestBodyBudget(maxBytes int64, perClientMax int) *requestBodyBudget {
	return &requestBodyBudget{maxBytes: maxBytes, perClientMax: perClientMax, perOwner: map[string]int64{}, perClient: map[string]int{}}
}

// newRequestBodySpoolBudget is the node spool budget with its per-owner share.
func newRequestBodySpoolBudget(maxBytes int64, perClientMax int) *requestBodyBudget {
	b := newRequestBodyBudget(maxBytes, perClientMax)
	b.ownerMax = maxBytes / api.RequestBodySpoolOwnerShareDivisor
	return b
}

// lease opens a hold for one request of owner (an app's resource owner, ""
// when unknown), taking one of its client's slots. A nil budget hands out
// leases that never refuse.
func (b *requestBodyBudget) lease(client, owner string) (*requestBodyLease, bool) {
	if b == nil {
		return &requestBodyLease{}, true
	}
	if b.perClientMax > 0 && client != "" {
		b.mu.Lock()
		if b.perClient[client] >= b.perClientMax {
			b.mu.Unlock()
			return nil, false
		}
		b.perClient[client]++
		b.mu.Unlock()
	}
	return &requestBodyLease{budget: b, client: client, owner: owner}, true
}

// reserve takes n bytes for owner; false when the node budget or the owner's
// share would be exceeded.
func (b *requestBodyBudget) reserve(n int64, owner string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used+n > b.maxBytes {
		return false
	}
	if b.ownerMax > 0 && owner != "" && b.perOwner[owner]+n > b.ownerMax {
		return false
	}
	b.used += n
	if b.ownerMax > 0 && owner != "" {
		b.perOwner[owner] += n
	}
	return true
}

// giveBack returns n bytes reserved for owner.
func (b *requestBodyBudget) giveBack(n int64, owner string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used -= n
	if b.ownerMax > 0 && owner != "" {
		if left := b.perOwner[owner] - n; left > 0 {
			b.perOwner[owner] = left
		} else {
			delete(b.perOwner, owner)
		}
	}
}

// usedBytes reports the bytes currently reserved.
func (b *requestBodyBudget) usedBytes() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// requestBodyLease is one request's share of a requestBodyBudget. release is
// idempotent, so every exit path may call it.
type requestBodyLease struct {
	budget *requestBodyBudget
	client string
	owner  string
	bytes  int64
	once   sync.Once
}

// grow takes n more bytes; false when the budget is spent.
func (l *requestBodyLease) grow(n int64) bool {
	if l.budget == nil || n <= 0 {
		return true
	}
	if !l.budget.reserve(n, l.owner) {
		return false
	}
	l.bytes += n
	return true
}

// shrink hands back bytes reserved but not used.
func (l *requestBodyLease) shrink(n int64) {
	if l.budget == nil || n <= 0 || n > l.bytes {
		return
	}
	l.bytes -= n
	l.budget.giveBack(n, l.owner)
}

func (l *requestBodyLease) release() {
	if l == nil || l.budget == nil {
		return
	}
	l.once.Do(func() {
		if l.bytes > 0 {
			l.budget.giveBack(l.bytes, l.owner)
		}
		if l.budget.perClientMax > 0 && l.client != "" {
			l.budget.mu.Lock()
			if l.budget.perClient[l.client] <= 1 {
				delete(l.budget.perClient, l.client)
			} else {
				l.budget.perClient[l.client]--
			}
			l.budget.mu.Unlock()
		}
	})
}

// requestBodyClient is the per-client key for body budgets: the trusted
// client address (an IPv6 /56, the smallest block a home connection usually
// gets, so one subscriber cannot multiply its slots across its /64s), or ""
// when the hop did not establish one.
func requestBodyClient(r *http.Request) string {
	ip, ok := TrustedClientIP(r)
	if !ok {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	mask := net.CIDRMask(api.RequestBodySpoolClientIPv6Prefix, 128)
	return (&net.IPNet{IP: ip.Mask(mask), Mask: mask}).String()
}

// requestBodyOwner is the per-owner key for the spool share: the app's
// resource owner, falling back to its account and then the app itself.
func requestBodyOwner(app App) string {
	switch {
	case app.ResourceOwnerID != "":
		return app.ResourceOwnerID
	case app.AccountID != "":
		return app.AccountID
	default:
		return app.ID
	}
}
