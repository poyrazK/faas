package gateway

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
)

// ServiceDiscoveryDNSChaosPolicy carries the verified caller and active
// scenario lease for one private service-name lookup.
type ServiceDiscoveryDNSChaosPolicy struct {
	RunID       string
	CallerAppID string
	Lease       chaos.Lease
}

// ServiceDiscoveryDNSChaosResolver returns a policy only when both the caller
// and target are members of the same live scenario run.
type ServiceDiscoveryDNSChaosResolver func(context.Context, string, string) (ServiceDiscoveryDNSChaosPolicy, error)

// ServiceDiscoveryDNSChaosObserver records one injected private DNS query.
type ServiceDiscoveryDNSChaosObserver func(runID, callerAppID, generation string, rule chaos.Rule)

type serviceDiscoveryDNSChaos struct {
	resolve ServiceDiscoveryDNSChaosResolver
	observe ServiceDiscoveryDNSChaosObserver
	ordinal atomic.Uint64
	// unavailableUntil (unix nanoseconds) skips policy reads after a failed
	// one, so a slow store does not delay every guest's private answers.
	unavailableUntil atomic.Int64
}

// WithScenarioDNSChaos enables bounded faults for private *.svc.gregale
// questions. It does not affect public DNS or .internal aliases.
func (h *ServiceDiscoveryDNSHandler) WithScenarioDNSChaos(resolve ServiceDiscoveryDNSChaosResolver, observe ServiceDiscoveryDNSChaosObserver) *ServiceDiscoveryDNSHandler {
	if resolve == nil {
		h.scenarioChaos = nil
		return h
	}
	h.scenarioChaos = &serviceDiscoveryDNSChaos{resolve: resolve, observe: observe}
	return h
}

func (h *ServiceDiscoveryDNSHandler) applyScenarioDNSChaos(ctx context.Context, w dns.ResponseWriter, req *dns.Msg, remote string) bool {
	controller := h.scenarioChaos
	if controller == nil || len(req.Question) == 0 || time.Now().UnixNano() < controller.unavailableUntil.Load() {
		return false
	}
	ordinal := controller.ordinal.Add(1)
	for _, question := range req.Question {
		target := serviceDiscoveryName(question.Name)
		if target == "" {
			continue
		}
		lookupCtx, cancel := context.WithTimeout(ctx, api.ServiceDNSChaosLookupTimeout)
		policy, err := controller.resolve(lookupCtx, remote, target)
		cancel()
		if err != nil {
			controller.unavailableUntil.Store(time.Now().Add(api.ServiceDNSChaosBackoff).UnixNano())
			h.log.Warn("guest DNS scenario chaos policy unavailable", "target", target, "err", err, "retry_after", api.ServiceDNSChaosBackoff)
			return false
		}
		if policy.RunID == "" || policy.CallerAppID == "" || !policy.Lease.ExpiresAt.After(time.Now()) {
			continue
		}
		latency := time.Duration(0)
		var terminal *chaos.Rule
		for _, rule := range policy.Lease.Rules {
			if !rule.IsDNS() || rule.To != target || (rule.From != "" && rule.From != policy.Lease.CallerWorkload) {
				continue
			}
			if err := chaos.ValidateRule(rule); err != nil {
				h.log.Error("gateway: invalid stored scenario DNS chaos rule", "run_id", policy.RunID, "err", err)
				h.writeRcode(w, req, dns.RcodeServerFailure)
				return true
			}
			if !chaos.Select(rule, ordinal, "") {
				continue
			}
			if controller.observe != nil {
				controller.observe(policy.RunID, policy.CallerAppID, policy.Lease.Generation, rule)
			}
			if rule.Kind == chaos.KindDNSLatency {
				if delay := time.Duration(rule.LatencyMS) * time.Millisecond; delay > latency {
					latency = delay
				}
			} else if terminal == nil {
				selected := rule
				terminal = &selected
			}
		}
		if latency > 0 {
			timer := time.NewTimer(latency)
			<-timer.C
		}
		if terminal == nil {
			if latency > 0 {
				return false
			}
			continue
		}
		switch terminal.Kind {
		case chaos.KindDNSTimeout:
			// Deliberately leave the request unanswered so the guest resolver
			// observes its normal DNS timeout and retry behavior.
		case chaos.KindDNSServFail:
			h.writeRcode(w, req, dns.RcodeServerFailure)
		case chaos.KindDNSNXDomain:
			h.writeRcodeWithAuthority(w, req, dns.RcodeNameError, true)
		}
		return true
	}
	return false
}
