package api

import (
	"net/url"
	"strings"
)

// WithProjectOrganization sends the client's project calls to the named
// company's projects: every /v1/projects path becomes the matching
// /v1/orgs/{organization}/projects path, which resolves project slugs in the
// company and checks the member's role (apps/projects review 2026-11-10,
// P3). An empty organization keeps personal paths.
func (c *Client) WithProjectOrganization(organization string) *Client {
	c.projectOrg = strings.TrimSpace(organization)
	return c
}

// ProjectOrganization is the company WithProjectOrganization selected.
func (c *Client) ProjectOrganization() string { return c.projectOrg }

// projectRoute rewrites a /v1/projects path for the selected company. Every
// project route has a company form, including the reviewed-plan, safe-release
// and source-ref ones (ADR-20261113090000).
func (c *Client) projectRoute(path string) (string, error) {
	if c.projectOrg == "" {
		return path, nil
	}
	bare, _, _ := strings.Cut(path, "?")
	if bare != "/v1/projects" && !strings.HasPrefix(bare, "/v1/projects/") {
		return path, nil
	}
	return "/v1/orgs/" + url.PathEscape(c.projectOrg) + strings.TrimPrefix(path, "/v1"), nil
}
