package api

import "regexp"

var appSlugRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)

// ValidAppSlug is the canonical public app-name contract shared by direct
// app creation and project workload admission.
func ValidAppSlug(slug string) bool {
	return appSlugRE.MatchString(slug)
}
