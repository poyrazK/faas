package api

import "time"

// DomainReadiness is a summary of independent domain activation checks. A
// verified TXT challenge alone never establishes routing or HTTPS readiness.
type DomainReadiness struct {
	Status    string `json:"status"`
	Ownership string `json:"ownership"`
	Routing   string `json:"routing"`
	TLS       string `json:"tls"`
}

// AssessDomainReadiness uses only the supplied observations. Callers must
// discard stale or obsolete-claim DNS checks before constructing the response.
func (d CustomDomainResponse) AssessDomainReadiness(at time.Time) DomainReadiness {
	r := DomainReadiness{Status: "awaiting_ownership", Ownership: "pending", Routing: "pending", TLS: d.CertStatus}
	if r.TLS == "" {
		r.TLS = "pending"
	}
	for _, c := range d.Checks {
		if c.Name == "points_to_gregale" {
			r.Routing = c.Status
		}
	}
	if d.VerificationExpiredAt(at) {
		r.Status, r.Ownership = "expired", "expired"
		return r
	}
	if d.Verified {
		r.Ownership = "verified"
	}
	if d.CertStatus == "dns_drifted" {
		r.Status, r.Routing = "needs_attention", "fail"
		return r
	}
	if !d.Verified {
		return r
	}
	if r.Routing == "fail" {
		r.Status = "fix_routing"
		return r
	}
	if r.Routing != "ok" {
		r.Status = "checking_dns"
		return r
	}
	r.Status = "preparing_https"
	for _, c := range d.Checks {
		if c.Status == "fail" && (c.Name == "caa_permits" || c.Name == "ipv6_conflict") {
			r.Status = "needs_attention"
			return r
		}
	}
	if d.certificateReadyAt(at) {
		r.Status = "ready"
	} else if r.TLS != "pending" && r.TLS != "renewing" {
		r.Status = "needs_attention"
	}
	return r
}

// HTTPSReadyAt supports older servers without a readiness summary, while new
// servers also require successful routing checks. Certificate expiry is always
// evaluated locally so a cached response cannot stay ready after expiration.
func (d CustomDomainResponse) HTTPSReadyAt(at time.Time) bool {
	if !d.Verified || !d.certificateReadyAt(at) {
		return false
	}
	return d.Readiness == nil || d.Readiness.Status == "ready" && d.Readiness.Ownership == "verified" && d.Readiness.Routing == "ok"
}

func (d CustomDomainResponse) certificateReadyAt(at time.Time) bool {
	if d.CertStatus != "" && d.CertStatus != "issued" && d.CertStatus != "renewing" {
		return false
	}
	expires := d.CertExpiresAt
	if expires == "" {
		expires = d.CertNotAfter
	}
	end, err := time.Parse(time.RFC3339Nano, expires)
	return err == nil && end.After(at)
}
