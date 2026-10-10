package gateway

// ADR-968: verified crawlers. A request is a verified bot only when its
// User-Agent claims a known crawler AND the client IP's reverse DNS name
// ends in that crawler's domain AND the name resolves back to the IP.
// Lookups run only for claimed bots on rules that read verified_bot, are
// cached per IP, bounded in time and concurrency, and fail closed.

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// BotResolver is the DNS surface verification needs; *net.Resolver
// implements it.
type BotResolver interface {
	LookupAddr(ctx context.Context, addr string) ([]string, error)
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// BotVerifier confirms claimed crawlers by forward-confirmed reverse DNS.
// It is safe for concurrent use.
type BotVerifier struct {
	resolver BotResolver
	now      func() time.Time
	inFlight chan struct{}

	mu    sync.Mutex
	cache map[string]botVerdict // key: bot name + "|" + ip
	calls map[string]*botCall
}

type botVerdict struct {
	ok      bool
	expires time.Time
}

type botCall struct {
	done chan struct{}
	ok   bool
}

// NewBotVerifier returns a verifier using resolver (nil = net.DefaultResolver).
func NewBotVerifier(resolver BotResolver) *BotVerifier {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &BotVerifier{
		resolver: resolver,
		now:      time.Now,
		inFlight: make(chan struct{}, api.EdgeRuleBotVerifyMaxInFlight),
		cache:    map[string]botVerdict{},
		calls:    map[string]*botCall{},
	}
}

// VerifiedBot returns the known crawler userAgent claims to be when ip
// verifies as that crawler, else "".
func (v *BotVerifier) VerifiedBot(ctx context.Context, ip net.IP, userAgent string) string {
	if v == nil || ip == nil {
		return ""
	}
	bot, ok := api.ClaimedEdgeRuleBot(userAgent)
	if !ok {
		return ""
	}
	if v.verify(ctx, ip, bot) {
		return bot.Name
	}
	return ""
}

func (v *BotVerifier) verify(ctx context.Context, ip net.IP, bot api.EdgeRuleKnownBot) bool {
	key := bot.Name + "|" + ip.String()
	v.mu.Lock()
	if verdict, ok := v.cache[key]; ok && v.now().Before(verdict.expires) {
		v.mu.Unlock()
		return verdict.ok
	}
	if call, ok := v.calls[key]; ok {
		v.mu.Unlock()
		select {
		case <-call.done:
			return call.ok
		case <-ctx.Done():
			return false
		}
	}
	select {
	case v.inFlight <- struct{}{}:
	default:
		// Budget spent: fail closed without caching, so the next request
		// can verify once load drops.
		v.mu.Unlock()
		return false
	}
	call := &botCall{done: make(chan struct{})}
	v.calls[key] = call
	v.mu.Unlock()

	// Detached from the request so one cancelled request does not poison
	// the shared result; bounded by the verify timeout instead.
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.EdgeRuleBotVerifyTimeout)
	ok, definitive := v.lookup(lookupCtx, ip, bot)
	cancel()
	<-v.inFlight

	v.mu.Lock()
	delete(v.calls, key)
	if definitive {
		ttl := api.EdgeRuleBotVerifyTTL
		if !ok {
			ttl = api.EdgeRuleBotVerifyNegativeTTL
		}
		v.storeLocked(key, botVerdict{ok: ok, expires: v.now().Add(ttl)})
	}
	v.mu.Unlock()
	call.ok = ok
	close(call.done)
	return ok
}

// lookup runs the forward-confirmed reverse DNS check. definitive is false
// when DNS failed transiently (timeout, server error), so the verdict is
// not cached.
func (v *BotVerifier) lookup(ctx context.Context, ip net.IP, bot api.EdgeRuleKnownBot) (ok, definitive bool) {
	names, err := v.resolver.LookupAddr(ctx, ip.String())
	if err != nil {
		return false, isNotFound(err)
	}
	for _, name := range names {
		host := strings.ToLower(strings.TrimSuffix(name, "."))
		if !hasBotSuffix(host, bot.DNSSuffixes) {
			continue
		}
		addrs, err := v.resolver.LookupIPAddr(ctx, host)
		if err != nil {
			if !isNotFound(err) {
				return false, false
			}
			continue
		}
		for _, a := range addrs {
			if a.IP.Equal(ip) {
				return true, true
			}
		}
	}
	return false, true
}

func hasBotSuffix(host string, suffixes []string) bool {
	for _, s := range suffixes {
		if strings.HasSuffix(host, s) && len(host) > len(s) {
			return true
		}
	}
	return false
}

func isNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

// storeLocked caches a verdict, dropping expired entries and then an
// arbitrary one when the cache is full.
func (v *BotVerifier) storeLocked(key string, verdict botVerdict) {
	if len(v.cache) >= api.EdgeRuleBotVerifyCacheSize {
		now := v.now()
		for k, e := range v.cache {
			if !now.Before(e.expires) {
				delete(v.cache, k)
			}
		}
		for k := range v.cache {
			if len(v.cache) < api.EdgeRuleBotVerifyCacheSize {
				break
			}
			delete(v.cache, k)
		}
	}
	v.cache[key] = verdict
}

// WithBotVerifier arms the verified_bot match field (ADR-968). nil leaves
// it absent.
func (h *Handler) WithBotVerifier(v *BotVerifier) *Handler {
	h.botVerifier = v
	return h
}

// SetBotVerifier installs crawler verification for the request (ADR-968).
// Call before the context is attached to a request; nil leaves the
// verified_bot field absent.
func (m *EdgeRuleMatchContext) SetBotVerifier(ctx context.Context, v *BotVerifier) {
	if v == nil {
		return
	}
	m.verifiedBot = func() string {
		m.botOnce.Do(func() {
			m.bot = v.VerifiedBot(ctx, m.ClientIP, m.Headers.Get("User-Agent"))
		})
		return m.bot
	}
}
