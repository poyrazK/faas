package agentproxy

import (
	"net/netip"
	"sync"
)

// Caps are the concurrent-connection caps (ADR-20261117185346 §4).
type Caps struct {
	PerVM            int
	PerAccountDomain int
	PerDomainFleet   int
}

// Refusal reasons, the outcome label of agent_egress_connections_total.
const (
	OutcomeAccepted         = "accepted"
	OutcomeCapVM            = "cap_vm"
	OutcomeCapAccountDomain = "cap_account_domain"
	OutcomeCapDomainFleet   = "cap_domain_fleet"
	OutcomeBlocklist        = "blocklist"
	OutcomeUnregistered     = "unregistered"
	OutcomeNoSNI            = "no_sni"
	OutcomeBadDestination   = "bad_destination"
	OutcomeUpstream         = "upstream_error"
)

// limiter holds the live connection counts. A connection over any cap is
// refused at once, never queued.
type limiter struct {
	caps Caps

	mu            sync.Mutex
	vm            map[netip.Addr]int
	accountDomain map[[2]string]int
	domain        map[string]int
}

func newLimiter(caps Caps) *limiter {
	return &limiter{caps: caps, vm: map[netip.Addr]int{}, accountDomain: map[[2]string]int{}, domain: map[string]int{}}
}

// acquire takes one connection for vm's account to domain, or names the cap
// it would pass. release returns it.
func (l *limiter) acquire(vm netip.Addr, account, domain string) (release func(), refused string) {
	ad := [2]string{account, domain}
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.vm[vm] >= l.caps.PerVM:
		return nil, OutcomeCapVM
	case l.accountDomain[ad] >= l.caps.PerAccountDomain:
		return nil, OutcomeCapAccountDomain
	case l.domain[domain] >= l.caps.PerDomainFleet:
		return nil, OutcomeCapDomainFleet
	}
	l.vm[vm]++
	l.accountDomain[ad]++
	l.domain[domain]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			decrement(l.vm, vm)
			decrement(l.accountDomain, ad)
			decrement(l.domain, domain)
		})
	}, ""
}

func decrement[K comparable](m map[K]int, k K) {
	if m[k] <= 1 {
		delete(m, k)
		return
	}
	m[k]--
}
