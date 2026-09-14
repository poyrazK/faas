package api

import "regexp"

var projectSlugRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidProjectSlug is the canonical project identity contract shared by the
// scan client, API boundary, and database constraint. Project slugs allow one
// to 63 characters; a hyphen may appear only between lowercase alphanumeric
// characters.
func ValidProjectSlug(slug string) bool {
	return projectSlugRE.MatchString(slug)
}
