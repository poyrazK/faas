// adr: 375
package hostidentity

import "strings"

// DefaultAppsDomain is the app writer's default public URL namespace. Operators
// can configure a different domain, including empty for custom-domain-only.
const DefaultAppsDomain = "gregale.dev"

// AppsSuffix returns the gateway's leading-dot comparison form.
func AppsSuffix(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" || strings.HasPrefix(domain, ".") {
		return domain
	}
	return "." + domain
}

// AppSlugFromHost performs ordinary app lookup's one-label extraction. Legacy
// slugs are not revalidated here: the router has always looked them up verbatim.
func AppSlugFromHost(suffix, host string) (string, bool) {
	if suffix == "" {
		return "", false
	}
	label, matched := strings.CutSuffix(host, suffix)
	if !matched || label == "" || strings.Contains(label, ".") {
		return "", false
	}
	return label, true
}

// BuildPrimaryAppHost returns a potential ordinary app URL. Immutable URLs
// resolve before slug lookup even when the apps and deployment domains differ.
// Legacy tag-prefixed slugs remain candidates: an alias can shadow that URL,
// but a missing alias falls through to ordinary lookup.
func BuildPrimaryAppHost(appsSuffix, slug string) string {
	host := slug + appsSuffix
	if parsed, ok := AppSlugFromHost(appsSuffix, host); !ok || parsed != slug {
		return ""
	}
	if _, _, ok := EnvironmentIDsFromHost(DeployWildcardSuffix, host); ok {
		return ""
	}
	if _, _, ok := DeploymentScopeFromHost(DeployWildcardSuffix, host); ok {
		return ""
	}
	return host
}
