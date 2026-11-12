package api

import (
	"net/http"
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

// personalOnlyProjectRoutes have no company form: the reviewed-plan and
// safe-release entry points and the source-ref scan.
var personalOnlyProjectRoutes = map[string]bool{
	"/v1/projects/apply-plan":      true,
	"/v1/projects/safe-deploy":     true,
	"/v1/projects/scan/source-ref": true,
}

// projectRoute rewrites a /v1/projects path for the selected company.
func (c *Client) projectRoute(path string) (string, error) {
	if c.projectOrg == "" {
		return path, nil
	}
	bare, _, _ := strings.Cut(path, "?")
	if bare != "/v1/projects" && !strings.HasPrefix(bare, "/v1/projects/") {
		return path, nil
	}
	if personalOnlyProjectRoutes[bare] {
		p := NewProblem(http.StatusBadRequest, CodeValidation, "Not available for organization projects",
			"this project operation has no organization form; run it without --org, or use the organization's scan and deploy").
			WithDocs(docsBase + "/orgs")
		return "", &APIError{Problem: *p}
	}
	return "/v1/orgs/" + url.PathEscape(c.projectOrg) + strings.TrimPrefix(path, "/v1"), nil
}
