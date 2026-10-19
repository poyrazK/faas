package api

import "strings"

// AppHostname is an app's default public hostname: <slug>.<apps domain>.
// "unset" and the legacy "apps.gregale.dev" both mean the public default
// gregale.dev. apid renders it in URLs and meterd's synthetic checks
// (ADR-748) request it, so the two can never disagree about an app's host.
func AppHostname(slug, appsDomain string) string {
	domain := strings.Trim(strings.TrimSpace(appsDomain), ".")
	if domain == "unset" || domain == "apps.gregale.dev" {
		domain = "gregale.dev"
	}
	if domain == "" {
		return slug
	}
	return slug + "." + domain
}
