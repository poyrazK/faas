// cmd/apid/custom_domain_routing.go — where customers point a custom domain
// (ADR-520) and what that means for the DNS probes.
//
// With self-hosted on-demand TLS the public edge must receive the customer's
// traffic directly: it completes the ACME challenge for the hostname and then
// terminates TLS with the certificate it obtained. The routing target is
// therefore FAAS_CUSTOM_DOMAIN_TARGET (a record that resolves straight to
// the edge) and, for zone apexes that cannot hold a CNAME, the edge
// addresses in FAAS_CUSTOM_DOMAIN_ADDRESSES. Without a configured target the
// apps-domain apex stays the target, which is the pre-ADR-520 contract.
package apidserver

import (
	"context"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) defaultDomainContext(ctx context.Context) context.Context {
	return state.WithCustomDomainRouting(ctx, s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled()))
}

// customDomainTarget returns the CNAME target customers publish.
func customDomainTarget() string {
	if target := api.CustomDomainTarget(); target != "" {
		return target
	}
	return strings.ToLower(strings.TrimSuffix(appsDomainFunc(), "."))
}

// customDomainCAAHost follows the certificate being requested: legacy mode
// mints a wildcard at the zone name, on-demand mode mints the concrete probe.
func customDomainCAAHost(domain string) (host string, wildcard bool) {
	if suffix, ok := state.WildcardDomainSuffix(domain); ok && !api.CustomDomainTLSOnDemand() {
		return suffix, true
	}
	host, _ = state.WildcardProbeHost(domain)
	return host, false
}

func customDomainCAARemediation(domain string) string {
	host, wildcard := customDomainCAAHost(domain)
	tag := "issue"
	if wildcard {
		tag = "issuewild"
	}
	return "Update CAA record at " + host + " to permit letsencrypt.org (e.g. '0 " + tag + " \"letsencrypt.org\"')"
}

// customDomainAddresses returns the edge addresses customers may use for
// A/AAAA records. apid validates the variable at boot, so a parse error here
// cannot happen in a running daemon.
func customDomainAddresses() []netip.Addr {
	addrs, _ := api.CustomDomainAddresses()
	return addrs
}

// customDomainDNSRecords returns the records the customer publishes for d:
// the TXT ownership proof, the routing CNAME and, when edge addresses are
// configured, the A/AAAA alternative for names that cannot hold a CNAME.
func customDomainDNSRecords(d state.CustomDomain) []api.DNSRecordInstruction {
	var out []api.DNSRecordInstruction
	if d.ChallengeToken != "" {
		out = append(out, api.DNSRecordInstruction{
			Type: "TXT", Name: state.CustomDomainChallengeName(d.Domain), Value: d.ChallengeToken,
			Purpose: api.DNSRecordPurposeVerification,
		})
	}
	if target := customDomainTarget(); target != "" {
		out = append(out, api.DNSRecordInstruction{
			Type: "CNAME", Name: d.Domain, Value: target, Purpose: api.DNSRecordPurposeRouting,
		})
	}
	for _, addr := range customDomainAddresses() {
		recordType := "A"
		if addr.Is6() {
			recordType = "AAAA"
		}
		out = append(out, api.DNSRecordInstruction{
			Type: recordType, Name: d.Domain, Value: addr.String(),
			Purpose: api.DNSRecordPurposeRouting, Alternative: true,
		})
	}
	return out
}

// routingRemediation is the doctor's "what to change" line for a domain that
// does not reach the edge.
func routingRemediation(domain, target string) string {
	line := "Set CNAME " + domain + " → " + target
	addrs := customDomainAddresses()
	if len(addrs) == 0 {
		return line
	}
	values := make([]string, len(addrs))
	for i, addr := range addrs {
		values[i] = addr.String()
	}
	return line + " (or, at a zone apex, A/AAAA → " + strings.Join(values, ", ") + ")"
}

// addressesAreEdge reports whether every observed address is a configured
// edge address. It is false when nothing was observed or no edge addresses
// are configured.
func addressesAreEdge(observed []string) bool {
	edge := customDomainAddresses()
	if len(observed) == 0 || len(edge) == 0 {
		return false
	}
	for _, raw := range observed {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			return false
		}
		found := false
		for _, e := range edge {
			if e == addr.Unmap() {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// resolvesToEdge reports whether domain's A and AAAA answers all belong to
// the configured edge, returning the observed addresses. Transient lookup
// failures leave the result pending rather than revoking a healthy domain.
func resolvesToEdge(ctx context.Context, domain string) (probeStatus, []string) {
	var observed []string
	pending := false
	for _, lookup := range []func(context.Context, string) ([]string, error){aLookupFunc, aaaaLookupFunc} {
		answers, err := lookup(ctx, domain)
		if err != nil {
			pending = pending || !isNotFound(err)
			continue
		}
		observed = append(observed, answers...)
	}
	if len(observed) > 0 && len(customDomainAddresses()) == 0 {
		// Without operator-supplied addresses, an A/AAAA alternative cannot
		// be classified. Absence and resolver failures are still evaluated.
		return probePending, observed
	}
	if len(observed) > 0 && !addressesAreEdge(observed) {
		return probeFail, observed
	}
	if pending {
		return probePending, observed
	}
	if addressesAreEdge(observed) {
		return probeOK, observed
	}
	return probeFail, observed
}

// withinOnDemandIssuanceGrace reports whether a verified domain is still in
// the window where the edge may be completing its first ACME order, so a
// failed port-443 probe is "pending", not "failed".
func withinOnDemandIssuanceGrace(d state.CustomDomain, now time.Time) bool {
	if !api.CustomDomainTLSOnDemand() || !d.Verified() {
		return false
	}
	return now.Sub(d.VerifiedAt) < time.Duration(api.OnDemandTLSIssuanceGraceSeconds)*time.Second
}

// startOnDemandTLSProbe runs the doctor for a just-verified domain in the
// background. Its port-443 handshake is what asks the edge to obtain the
// certificate, and it records the outcome. The probe is detached from the
// poller tick so it is not cut short when the tick ends, and bounded by the
// per-domain doctor budget.
func (s *server) startOnDemandTLSProbe(ctx context.Context, log *slog.Logger, domain string) {
	go func() {
		probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeTimeout+2*time.Second)
		defer cancel()
		if err := s.runDoctorForDomain(probeCtx, log, domain); err != nil {
			log.Warn("dns_poller: on-demand tls probe failed", "domain", domain, "err", err)
		}
	}()
}

// onDemandTLSAdvice replaces the doctor's TLS-check copy for custom domains
// served by ADR-520's edge. There the certificate is issued on the first
// HTTPS connection and the customer can act only on DNS, CAA records and
// proxies; the cert-engine wording describes tenant surfaces, which keep it.
// ok is false when the default copy already fits.
func onDemandTLSAdvice(d state.CustomDomain, obs state.DomainDoctorObservation) (detail, remediation string, ok bool) {
	if !api.CustomDomainTLSOnDemand() || obs.SurfaceID != "" {
		return "", "", false
	}
	target := customDomainTarget()
	pointAtEdge := "Make sure " + d.Domain + " resolves straight to " + target +
		" (DNS only, no CDN proxy) and that any CAA records allow letsencrypt.org; Gregale retries automatically."
	switch obs.CertState {
	case certStatusPending:
		return "certificate not issued yet",
			"Gregale requests it on the first HTTPS connection once " + d.Domain + " resolves to " + target + "; check again in a minute.", true
	case certStatusFailed:
		return "certificate issuance failed: " + obs.LastError, pointAtEdge, true
	case certStatusDialFailed:
		return "HTTPS check on port 443 failed: " + obs.LastError, pointAtEdge, true
	case certStatusCDN:
		return "a CDN or proxy answers for " + d.Domain + " with its own certificate",
			"Turn off the proxy for this record (DNS only) so it resolves to " + target + ".", true
	case "", "none":
		if d.Verified() {
			return "certificate pending issuance",
				"Gregale requests it on the first HTTPS connection once " + d.Domain + " resolves to " + target + ".", true
		}
	}
	return "", "", false
}
