package apidserver

import "strings"

// WithPlatformDomain sets the domain whose apex, api. and operations. hosts
// serve the site, dashboard and API (ADR-20261026100000). Empty keeps the
// apps domain in that role, the layout before the split.
func (s *server) WithPlatformDomain(domain string) *server {
	s.platformDomain = domain
	return s
}

// platformHost is the domain platform emails, inbound webhook URLs and
// dashboard origins name: the platform domain, else the apps domain.
func (s *server) platformHost() string {
	if s.platformDomain != "" {
		return s.platformDomain
	}
	return s.domain
}

// platformDomainFromConfig is the configured platform domain, or empty when
// it follows the apps domain or the layout does not resolve (run() has
// already refused to start on that).
func platformDomainFromConfig(cfg *Config, getenv func(string) string) string {
	domains, err := cfg.Domains(getenv)
	if err != nil || domains.Platform == domains.Apps {
		return ""
	}
	return domains.Platform
}

// platformOwnedDomain reports whether domain is the apps or platform domain,
// or a name or wildcard under either. Those were accepted as pending custom
// domain claims: no customer can verify them, but another account held a
// pending claim on the platform apex (Hunt 34 H34-51).
func (s *server) platformOwnedDomain(domain string) bool {
	name := strings.TrimPrefix(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), "."), "*.")
	for _, owned := range []string{s.domain, s.platformDomain} {
		owned = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(owned)), ".")
		if owned != "" && (name == owned || strings.HasSuffix(name, "."+owned)) {
			return true
		}
	}
	return false
}
