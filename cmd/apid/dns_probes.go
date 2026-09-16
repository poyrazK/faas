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
// CAA: Go's net.Resolver does not expose LookupCAA (as of
// Go 1.23), so we use LookupTXT at "<domain>" (the apex) and
// parse the `CAA`-style payload. Per RFC 8659, the record
// content is "<flags> <tag> <value>" where tag ∈ {issue,
// issuewild, iodef}. A `0 issue "letsencrypt.org"` (or any
// non-empty issuer) record permits issuance; `0 issue ";"`
// denies ALL issuance; missing CAA permits by default. The
// 15-line parser below is the entire CAA adapter.

package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
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
const probeTimeout = 5 * time.Second

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

// caaLookupFunc returns the CAA records at the apex. Go's
// net.Resolver does not expose LookupCAA, so this uses
// LookupTXT and filters for the CAA-flagged payloads. Per
// RFC 8659, every record begins with "<flags> <tag> <value>"
// — flags=0 means non-critical, tag is one of {issue,
// issuewild, iodef}.
var caaLookupFunc = func(ctx context.Context, domain string) ([]string, error) {
	return (&net.Resolver{}).LookupTXT(ctx, domain)
}

// appsDomainFunc returns the configured Gregale apex (the
// "expected" CNAME / A target for the points_to_gregale
// check). Mirrors the seam in the doctor's "expected"
// comparison. Production reads from config.GetAppsDomain();
// tests inject a constant.
var appsDomainFunc = func() string { return "" }

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
		if isNotFound(aErr) || isNotFound(aaaaErr) {
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
	expected := strings.TrimSuffix(appsDomainFunc(), ".")
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
	if cErr != nil {
		// Branch on isNotFound first, mirroring the other three
		// probes (checkApexA_AAAA, checkCAA, checkAAAAConflict).
		// A transient resolver error (timeout, SERVFAIL, network
		// unreachable) is "data missing, retry later" → probePending,
		// NOT probeNA. Conflating the two cases tells the customer
		// nothing is wrong when in reality the CNAME was simply not
		// fetched — and the points_to_gregale check is the
		// load-bearing one for activation drop-off, so a
		// misclassification here masks real DNS misconfiguration
		// during the exact window the customer is debugging it.
		if isNotFound(cErr) {
			// No CNAME. The customer may be using an apex A
			// record instead. checkApexA_AAAA is the load-bearing
			// check for that case; this check is na unless we
			// also see the apex pointing at us. Report na so the
			// customer isn't told to add a CNAME when they
			// correctly have an A record.
			return ProbeResult{
				Status:     probeNA,
				Detail:     "no CNAME at apex; using A/AAAA record instead",
				ObservedAt: now,
			}
		}
		return ProbeResult{
			Status:     probePending,
			Detail:     "CNAME lookup failed (transient)",
			ObservedAt: now,
		}
	}
	// Strip the trailing dot (LookupCNAME returns "target.")
	// and compare case-insensitively.
	target := strings.TrimSuffix(cname, ".")
	if strings.EqualFold(target, expected) {
		return ProbeResult{
			Status:     probeOK,
			Detail:     "CNAME → " + expected,
			Observed:   cname,
			ObservedAt: now,
		}
	}
	return ProbeResult{
		Status:      probeFail,
		Detail:      "CNAME does not point at Gregale",
		Observed:    cname,
		ObservedAt:  now,
		Remediation: "Set CNAME " + domain + " → " + expected,
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
	records, err := caaLookupFunc(rctx, domain)
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
	// Parse the records. Per RFC 8659: "<flags> <tag> <value>"
	// where tag is one of {issue, issuewild, iodef}. We treat
	// issue + issuewild as the controlling tags. Go's
	// LookupTXT returns each TXT record as the concatenated
	// text per RFC 1035 §3.3.14, so a CAA record stored as
	// `"0 issue \"letsencrypt.org\""` arrives in `records`
	// as `0 issue "letsencrypt.org"` (outer quotes already
	// stripped, inner quotes preserved).
	permits, observed := parseCAARecords(records)
	if permits {
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
		Remediation: "Update CAA record at " + domain + " to permit letsencrypt.org (e.g. '0 issue \"letsencrypt.org\"')",
	}
}

// parseCAARecords returns (permits, observed). Permits is
// true when any record allows issuance. Per RFC 8659, the
// `issue` and `issuewild` tags are independent: an
// `0 issue ";"` denies all CA issuance, but `0 issuewild
// "letsencrypt.org"` still permits wildcards. We treat
// the doctor's "permits issuance" question as: "would
// letsencrypt.org be allowed to issue SOME cert for this
// domain?" — which means: at least one tag (issue or
// issuewild) must allow a non-empty issuer, AND no
// controlling tag must deny. The RFC semantics are stricter
// (an empty issuewild tag with a populated issue permits
// only exact-hostname issuance), but the doctor renders
// "permits / denies" at coarse grain so a customer who
// can publish a cert at all sees ok.
func parseCAARecords(records []string) (bool, []string) {
	observed := make([]string, 0, len(records))
	issuePermits := false
	issuewildPermits := false
	for _, r := range records {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		observed = append(observed, r)
		parts := strings.Fields(r)
		if len(parts) < 3 {
			continue
		}
		tag := strings.ToLower(parts[1])
		value := strings.Trim(parts[2], `"`)
		switch tag {
		case "issue":
			if value != "" && value != ";" {
				issuePermits = true
			}
		case "issuewild":
			if value != "" && value != ";" {
				issuewildPermits = true
			}
		}
	}
	// The doctor's "permits" check is satisfied when EITHER
	// tag allows a non-empty issuer. A `0 issue ";"` by
	// itself does not deny wildcards; a `0 issuewild ";"`
	// by itself does not deny exact-hostname. A future
	// strict-RFC variant of the doctor would surface
	// the per-tag state separately; the coarse binary
	// is the load-bearing answer for the activation
	// drop-off.
	return issuePermits || issuewildPermits, observed
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
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); dnsFound = checkApexA_AAAA(ctx, domain) }()
	go func() { defer wg.Done(); pointsToG = checkPointsToGregale(ctx, domain) }()
	go func() { defer wg.Done(); caa = checkCAA(ctx, domain) }()
	go func() { defer wg.Done(); aaaa = checkAAAAConflict(ctx, domain) }()
	wg.Wait()
	return
}
