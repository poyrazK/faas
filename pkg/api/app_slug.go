package api

import (
	"regexp"
	"strings"
)

var appSlugRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)

var reservedAppSlugs = map[string]struct{}{
	"account": {}, "admin": {}, "api": {}, "assets": {}, "billing": {},
	"cdn": {}, "console": {}, "dashboard": {}, "docs": {}, "help": {},
	"login": {}, "logout": {}, "mail": {}, "ns": {}, "operations": {},
	"security": {}, "signup": {}, "static": {}, "status": {}, "support": {},
	"www": {},
}

// ValidAppSlug is the canonical public app-name contract shared by direct
// app creation and project workload admission.
func ValidAppSlug(slug string) bool {
	return appSlugRE.MatchString(slug) && !strings.HasPrefix(slug, "tag-")
}

// IsReservedAppSlug reports names held for Gregale-owned services. Existing
// rows remain readable so operators can migrate an accidental collision, but
// every public allocation path must reject these names.
func IsReservedAppSlug(slug string) bool {
	_, ok := reservedAppSlugs[slug]
	return ok || IsPreviewAppSlug(slug)
}

// IsPreviewAppSlug reports the pr-<N>-<parent> shape PR previews are
// allocated under (and that pr-<N>-<parent>.<apps domain> routes to). Only
// preview provisioning may create it: an ordinary app holding
// "pr-7-shop" made the shop app's PR #7 preview fail with "slug taken" and
// left the preview hostname serving someone else's app.
func IsPreviewAppSlug(slug string) bool {
	rest, ok := strings.CutPrefix(slug, "pr-")
	if !ok {
		return false
	}
	digits, parent, ok := strings.Cut(rest, "-")
	if !ok || digits == "" || parent == "" || (digits[0] == '0' && len(digits) > 1) {
		return false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	return true
}
