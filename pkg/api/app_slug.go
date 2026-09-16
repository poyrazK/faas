package api

import "regexp"

var appSlugRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)

var reservedAppSlugs = map[string]struct{}{
	"account": {}, "admin": {}, "api": {}, "assets": {}, "billing": {},
	"cdn": {}, "console": {}, "dashboard": {}, "docs": {}, "help": {},
	"login": {}, "logout": {}, "mail": {}, "operations": {},
	"security": {}, "signup": {}, "static": {}, "status": {}, "support": {},
	"www": {},
}

// ValidAppSlug is the canonical public app-name contract shared by direct
// app creation and project workload admission.
func ValidAppSlug(slug string) bool {
	return appSlugRE.MatchString(slug)
}

// IsReservedAppSlug reports names held for Gregale-owned services. Existing
// rows remain readable so operators can migrate an accidental collision, but
// every public allocation path must reject these names.
func IsReservedAppSlug(slug string) bool {
	_, ok := reservedAppSlugs[slug]
	return ok
}
