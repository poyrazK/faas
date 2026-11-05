// cmd/apid/dns_probes.go — the domain doctor probe engine
// (ADR-120, issue #961 follow-on).
//
// Five probes that map 1:1 to the Render-style "domain doctor"
// lines (DNS record found / points to Gregale / TLS / CAA /
// IPv6 conflict). The probes are tiny, pure, and side-effect
// free; the dns_poller calls them in a single goroutine on its
// 30 s tick, and the doctor handler calls them via
// runProbesParallel() when the cached observation row is older
// than FAAS_DOMAIN_DOCTOR_TTL_SECONDS (default 300 s).
//
// Test seams (mirroring cmd/apid/dns_verify.go:51,86 and
// cmd/apid/dns_poller.go:184) — production uses real
// net.Resolver / tls.Dial; tests inject fakes. Each seam is a
// package-level `var` that returns a typed result so test
// injection is a one-line swap.
//
// CAA queries and issuer policy live in pkg/apid/domaindns.

package apidserver

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/domaindns"
	"github.com/onebox-faas/faas/pkg/state"
)

// probeStatus is the closed set of probe outcomes. Mirrors the
// DomainDoctorCheck.Status wire field. Reused across probes so
// goconst (package-wide) doesn't trip on literal "ok" / "fail"
// strings in handlers_ext.go.
type probeStatus string

const (
	probeOK      probeStatus = "ok"
	probeFail    probeStatus = "fail"
	probePending probeStatus = "pending"
	probeNA      probeStatus = "na"
)

// ProbeResult is what each probe returns. Detail is the
// human-readable line; Observed is the raw value (e.g. CNAME
// target) so the doctor handler can render it without
// re-querying; Remediation is the exact record to change when
// the probe failed and we know what the customer should set.
type ProbeResult struct {
	Status      probeStatus
	Detail      string
	Observed    string
	ObservedAt  time.Time
	Remediation string
}

// probeTimeout is the per-probe budget. Matches the existing
// dialCertTimeout (5s) at cmd/apid/dns_verify.go:81 so the
// synchronous re-probe path never blows past the request
// budget. The poller also respects this — a probe that takes
// > 5s gets the same ctx-cancel + joinErr treatment dialCert
// has, so a misconfigured DNS server can't wedge the
// goroutine.
const probeTimeout = api.DomainDoctorDNSTimeout

// --- Test seams. Production = real net.Resolver. --------------

// aLookupFunc returns the A records for domain. The doctor
// uses this for the "DNS record found" check: any A or AAAA
// at the apex is a hit (a domain with no apex records is
// either not delegated or pointing at a CNAME only — both
// are healthy in different ways; the points_to_gregale
// check is the load-bearing one).
var aLookupFunc = func(ctx context.Context, domain string) ([]string, error) {
	ips, err := (&net.Resolver{}).LookupIP(ctx, "ip4", domain)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ips))
	for i, ip := range ips {
		out[i] = ip.String()
	}
	return out, nil
}

// aaaaLookupFunc returns the AAAA records. The doctor uses
// this twice: (a) for the "DNS record found" check (any A
// or AAAA is a hit); (b) for the "IPv6 conflict" check (a
// stray AAAA at the apex that doesn't match the CNAME
// target is the conflict class).
var aaaaLookupFunc = func(ctx context.Context, domain string) ([]string, error) {
	ips, err := (&net.Resolver{}).LookupIP(ctx, "ip6", domain)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ips))
	for i, ip := range ips {
		out[i] = ip.String()
	}
	return out, nil
}

// caaLookupFunc queries the relevant CAA RRset through the system resolvers.
var caaLookupFunc = domaindns.LookupCAA

// appsDomainFunc returns the configured Gregale apex (the
// "expected" CNAME / A target for the points_to_gregale
// check). Mirrors the seam in the doctor's "expected"
// comparison. Production reads from config.GetAppsDomain();
// tests inject a constant.
var appsDomainFunc = func() string { return "" }

// ownedDomainsFunc returns every domain Gregale owns (ADR-20261026100000).
// run() wires the configured layout. Without a dedicated custom-domain
// target, earlier owned apexes remain valid across an apps-domain move.
var ownedDomainsFunc = func() []string { return nil }

// isOwnedApex reports whether target is the apex of an owned domain.
func isOwnedApex(target string) bool {
	return slices.ContainsFunc(ownedDomainsFunc(), func(domain string) bool { return strings.EqualFold(domain, target) })
}

// --- The five probes -----------------------------------------

// checkApexA_AAAA answers the "DNS record found" Render-style
// line. Any A or AAAA record at the apex is a hit. If
// neither exists the customer has not delegated the domain
// at all (or has delegated it as a CNAME-only — in which
// case the points_to_gregale check is the load-bearing one
// and this check returns pending, not fail).
func checkApexA_AAAA(ctx context.Context, domain string) ProbeResult {
	rctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	aRecs, aErr := aLookupFunc(rctx, domain)
	aaaaRecs, aaaaErr := aaaaLookupFunc(rctx, domain)
	now := time.Now().UTC()
	if aErr != nil && aaaaErr != nil {
		// Both lookups failed. Distinguish NXDOMAIN (definite
		// "not delegated", fail) from a transient resolver
		// error (pending — could be a flaky upstream). Go
		// returns *net.DNSError; check for NXDOMAIN/NOERROR
		// shape — net.DNSError.IsNotFound is the canonical
		// predicate.
		if isNotFound(aErr) && isNotFound(aaaaErr) {
			return ProbeResult{
				Status:      probeFail,
				Detail:      "no A or AAAA records published at " + domain,
				Observed:    "",
				ObservedAt:  now,
				Remediation: "Publish an A or AAAA record at " + domain,
			}
		}
		return ProbeResult{
			Status:     probePending,
			Detail:     "DNS resolver did not respond (transient)",
			ObservedAt: now,
		}
	}
	// At least one lookup succeeded.
	var observed []string
	observed = append(observed, aRecs...)
	observed = append(observed, aaaaRecs...)
	return ProbeResult{
		Status:     probeOK,
		Detail:     "A and AAAA records present",
		Observed:   strings.Join(observed, ","),
		ObservedAt: now,
	}
}

// checkPointsToGregale answers "is the customer's DNS
// pointing at Gregale?". Compares the CNAME target (or the
// apex A/AAAA if no CNAME) to the configured Gregale apex
// (Config.GetAppsDomain()). A CNAME loop or a CNAME pointing
// at a third party is fail with a remediation line.
func checkPointsToGregale(ctx context.Context, domain string) ProbeResult {
	rctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cname, cErr := cnameLookupFunc(rctx, domain)
	now := time.Now().UTC()
	expected := customDomainTarget()
	if expected == "" {
		// Operator hasn't set FAAS_APPS_DOMAIN. Degrade to
		// pending rather than fail — the customer can't be
		// expected to fix an operator-side config issue.
		return ProbeResult{
			Status:     probePending,
			Detail:     "FAAS_APPS_DOMAIN not configured; points_to_gregale cannot be evaluated",
			ObservedAt: now,
		}
	}
	if cErr != nil && !isNotFound(cErr) {
		return ProbeResult{
			Status:     probePending,
			Detail:     "CNAME lookup failed (transient)",
			ObservedAt: now,
		}
	}
	// A missing CNAME may have A/AAAA alternatives. Evaluate those below:
	// definite absence of all routing records must revoke verification.
	// Strip the trailing dot (LookupCNAME returns "target.")
	// and compare case-insensitively.
	target := strings.TrimSuffix(cname, ".")
	if cErr == nil && (strings.EqualFold(target, expected) || (api.CustomDomainTarget() == "" && isOwnedApex(target))) {
		return ProbeResult{
			Status:     probeOK,
			Detail:     "CNAME → " + strings.ToLower(target),
			Observed:   cname,
			ObservedAt: now,
		}
	}
	// ADR-520: an apex A/AAAA record (Go reports the name itself as its
	// canonical name) or a flattened CNAME is fine when every address is
	// a configured edge address.
	status, observed := probeFail, []string(nil)
	if cErr != nil || len(customDomainAddresses()) > 0 {
		status, observed = resolvesToEdge(rctx, domain)
	}
	if status == probePending {
		detail := "address lookup failed (transient)"
		if len(observed) > 0 && len(customDomainAddresses()) == 0 {
			detail = "configure FAAS_CUSTOM_DOMAIN_ADDRESSES to validate A/AAAA routing"
		}
		return ProbeResult{Status: probePending, Detail: detail, ObservedAt: now}
	}
	if status == probeOK {
		return ProbeResult{
			Status:     probeOK,
			Detail:     "A/AAAA → Gregale edge",
			Observed:   strings.Join(observed, ","),
			ObservedAt: now,
		}
	}
	return ProbeResult{
		Status:      probeFail,
		Detail:      "CNAME does not point at Gregale",
		Observed:    cname,
		ObservedAt:  now,
		Remediation: routingRemediation(domain, expected),
	}
}

// checkCAA answers "does the customer's DNS permit certificate
// issuance?". A `0 issue "letsencrypt.org"` record permits; a
// `0 issue ";"` record denies ALL issuance; missing CAA
// permits by default (handled by the apid handler's tri-state
// read of the caa_permits column — null = no CAA = permitted).
func checkCAA(ctx context.Context, domain string) ProbeResult {
	rctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	caaHost, wildcard := customDomainCAAHost(domain)
	records, err := caaLookupFunc(rctx, caaHost)
	now := time.Now().UTC()
	if err != nil {
		if isNotFound(err) {
			// No CAA published at apex. This is the healthy
			// default — return a result with empty Observed
			// and let the handler map it to NULL in the
			// caa_permits column. The Status is OK because
			// "no CAA = permitted" is the customer's healthy
			// state, not a failure.
			return ProbeResult{
				Status:     probeOK,
				Detail:     "no CAA published (allowed by default)",
				Observed:   "",
				ObservedAt: now,
			}
		}
		return ProbeResult{
			Status:     probePending,
			Detail:     "CAA lookup failed (transient)",
			ObservedAt: now,
		}
	}
	// The lookup returns CAA records, including an inherited or aliased RRset.
	challenges := []string{"dns-01"}
	if api.CustomDomainTLSOnDemand() {
		challenges = []string{"http-01", "tls-alpn-01"}
	}
	permission := domaindns.LetsEncryptPermission(records, challenges)
	if wildcard {
		permission = domaindns.LetsEncryptWildcardPermission(records, challenges)
	}
	observed := permission.Observed
	if permission.Unknown {
		return ProbeResult{Status: probePending, Detail: "CAA issuer restrictions require confirmation against the edge ACME account", Observed: strings.Join(observed, ";"), ObservedAt: now}
	}
	if permission.Permits {
		return ProbeResult{
			Status:     probeOK,
			Detail:     "CAA permits certificate issuance",
			Observed:   strings.Join(observed, ";"),
			ObservedAt: now,
		}
	}
	return ProbeResult{
		Status:      probeFail,
		Detail:      "CAA denies certificate issuance for this CA",
		Observed:    strings.Join(observed, ";"),
		ObservedAt:  now,
		Remediation: customDomainCAARemediation(domain),
	}
}

// parseCAARecords checks Let's Encrypt's concrete-host certificates. A
// wildcard domain also receives individual host certificates (ADR-520).
func parseCAARecords(records []string) (bool, []string) {
	return domaindns.PermitsLetsEncrypt(records)
}

// checkAAAAConflict answers "is there a stray AAAA record at
// the apex that conflicts with the customer's CNAME?". A
// stray AAAA is a known activation footgun: the customer
// adds a CNAME pointing at Gregale, but a stale AAAA from
// a prior hosting provider still resolves. Per RFC 1034
// §3.6.2, when both a CNAME and any other record type
// exist at the same name, the CNAME should be the
// canonical answer; in practice many resolvers return
// both, and the AAAA wins some fraction of requests.
func checkAAAAConflict(ctx context.Context, domain string) ProbeResult {
	rctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	aaaa, err := aaaaLookupFunc(rctx, domain)
	now := time.Now().UTC()
	if err != nil {
		if isNotFound(err) {
			return ProbeResult{
				Status:     probeOK,
				Detail:     "no AAAA record at apex",
				ObservedAt: now,
			}
		}
		return ProbeResult{
			Status:     probePending,
			Detail:     "AAAA lookup failed (transient)",
			ObservedAt: now,
		}
	}
	if len(aaaa) == 0 {
		return ProbeResult{
			Status:     probeOK,
			Detail:     "no AAAA record at apex",
			ObservedAt: now,
		}
	}
	if addressesAreEdge(aaaa) {
		return ProbeResult{
			Status:     probeOK,
			Detail:     "AAAA → Gregale edge",
			Observed:   strings.Join(aaaa, ","),
			ObservedAt: now,
		}
	}
	return ProbeResult{
		Status:      probeFail,
		Detail:      "AAAA record at apex conflicts with CNAME",
		Observed:    strings.Join(aaaa, ","),
		ObservedAt:  now,
		Remediation: "Remove AAAA record at " + domain,
	}
}

// isNotFound reports whether err is a *net.DNSError with
// IsNotFound=true. Centralised so the probes don't each
// duplicate the type-assertion.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var dnsErr *net.DNSError
	if errorsAs(err, &dnsErr) {
		return dnsErr.IsNotFound
	}
	return false
}

// errorsAs is a tiny local errors.As shim that handles
// wrapped errors (the underlying error may be a *net.DNSError
// wrapped via fmt.Errorf("...: %w", err)). Mirrors the
// migrations test's hand-rolled errorsAs but uses the standard
// library's `errors.As` underneath, which already walks the
// Unwrap chain. The shim is here so callers don't need to know
// the target type.
func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}

// --- Parallel fan-out ----------------------------------------

// runProbesParallel executes the four DNS-side probes in
// parallel (the cert check is the 5th probe and is handled
// separately by the caller because it has its own dialCert
// budget + error classification). Used only by the doctor
// handler's synchronous re-probe path and by each bounded poller worker.
//
// 5 s budget per probe (matches probeTimeout). If a probe
// returns within the budget, its result is recorded; if
// the budget elapses, the probe is reported as pending
// (the customer can re-poll).
func runProbesParallel(ctx context.Context, domain string) (dnsFound, pointsToG, caa, aaaa ProbeResult) {
	probeDomain, _ := state.WildcardProbeHost(domain)
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); dnsFound = checkApexA_AAAA(ctx, probeDomain) }()
	go func() { defer wg.Done(); pointsToG = checkPointsToGregale(ctx, probeDomain) }()
	go func() { defer wg.Done(); caa = checkCAA(ctx, domain) }()
	go func() { defer wg.Done(); aaaa = checkAAAAConflict(ctx, probeDomain) }()
	wg.Wait()
	return
}
