package api

import "regexp"

var projectEnvironmentSlugRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,31}[a-z0-9])?$`)

// ValidProjectEnvironmentSlug is the public contract for durable project
// environment names. Environment names are deliberately shorter than project
// slugs so they remain safe in deployment and promotion paths. The default
// app scope is reserved and cannot also name a project environment, and so is
// the PR preview scope (ADR-20261028090000), whose values reach code under
// review.
func ValidProjectEnvironmentSlug(slug string) bool {
	return slug != DefaultEnvScope && slug != PreviewEnvScope && projectEnvironmentSlugRE.MatchString(slug)
}
