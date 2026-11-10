package middleware

import (
	"context"
	"net/http"
	"strings"
)

// orgProjectPathKey carries the organization slug of an
// /v1/orgs/{organization}/projects/{slug}/... request that apid dispatched to
// the matching /v1/projects/{slug}/... handler (apps/projects review
// 2026-11-10, P1). Only apid's org project mirror sets it.
type orgProjectPathKey struct{}

// WithOrganizationProjectPath records that the request addressed the project
// under organization's path.
func WithOrganizationProjectPath(ctx context.Context, organization string) context.Context {
	return context.WithValue(ctx, orgProjectPathKey{}, organization)
}

// OrganizationSlug is the organization a project or app request addresses:
// the {organization} path value, or the organization of a mirrored org
// project path. It is "" for a personal path.
func OrganizationSlug(r *http.Request) string {
	if organization := r.PathValue("organization"); organization != "" {
		return organization
	}
	organization, _ := r.Context().Value(orgProjectPathKey{}).(string)
	return organization
}

// orgPathPrefix reports whether path is under /v1/orgs/{organization}/ and
// returns the rest after the organization segment.
func orgPathRest(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/v1/orgs/{organization}")
	return rest, ok
}
