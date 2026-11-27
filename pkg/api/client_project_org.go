package api

import (
	"net/http"
	"net/url"
	"strings"
)

// WithProjectOrganization selects the company the client works for (the CLI's
// global --org). Project calls go to the company's projects, and the
// account-wide calls that have a company form (creating apps, managed
// PostgreSQL, buckets, plan and billing) go to the company instead of the
// caller's personal account; see orgRoute. An empty organization keeps
// personal paths.
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

// orgAccountRoutes maps an account-wide path (exact, or every path below it
// when it ends in "/") to its company form below /v1/orgs/{organization}.
// App-scoped paths (/v1/apps/{slug}/...) need no mapping: app slugs are
// global and the server authorizes the member's role on the company app.
var orgAccountRoutes = []struct{ personal, org string }{
	{"/v1/account/plan", "/plan"},
	{"/v1/account/overage-cap", "/overage-cap"},
	{"/v1/account/egress-overage", "/egress-overage"},
	{"/v1/billing/portal", "/billing/portal"},
	{"/v1/billing/status", "/billing/status"},
	{"/v1/billing/costs", "/billing/costs"},
	{"/v1/billing/budgets", "/billing/budgets"},
	{"/v1/billing/budgets/", "/billing/budgets/"},
	{"/v1/postgres/", "/postgres/"},
}

// orgRoute is projectRoute plus the company form of account-wide calls
// (ADR-20261127200000). With a company selected:
//   - POST /v1/apps creates the app in the company;
//   - POST /v1/apps/{slug}/buckets creates the bucket through the company;
//   - plan, billing, budget, overage and managed PostgreSQL paths go to the
//     company's routes;
//   - any other write to the personal account (/v1/account, /v1/billing,
//     /v1/keys, /v1/usage) is refused before it is sent, so --org never
//     quietly acts on the personal account.
func (c *Client) orgRoute(method, path string) (string, error) {
	if c.projectOrg == "" {
		return path, nil
	}
	path, err := c.projectRoute(path)
	if err != nil || strings.HasPrefix(path, "/v1/orgs/") {
		return path, err
	}
	bare, query, hasQuery := strings.Cut(path, "?")
	suffix := ""
	if hasQuery {
		suffix = "?" + query
	}
	orgBase := "/v1/orgs/" + url.PathEscape(c.projectOrg)
	if bare == "/v1/apps" && method == http.MethodPost {
		return orgBase + "/apps" + suffix, nil
	}
	if rest, ok := strings.CutPrefix(bare, "/v1/apps/"); ok && method == http.MethodPost {
		if slug, tail, _ := strings.Cut(rest, "/"); slug != "" && tail == "buckets" {
			return orgBase + "/apps/" + slug + "/buckets" + suffix, nil
		}
	}
	if rest, ok := strings.CutPrefix(bare, "/v1/postgres/"); ok && postgresFlatOnly(rest) {
		return path, nil
	}
	// Budget alert channels have no organization form yet: reads stay
	// personal and writes are refused below.
	budgetChannels := strings.HasPrefix(bare, "/v1/billing/budgets/") && strings.HasSuffix(bare, "/channels")
	for _, route := range orgAccountRoutes {
		if budgetChannels {
			break
		}
		if strings.HasSuffix(route.personal, "/") {
			if rest, ok := strings.CutPrefix(bare, route.personal); ok {
				return orgBase + route.org + rest + suffix, nil
			}
			continue
		}
		if bare == route.personal {
			return orgBase + route.org + suffix, nil
		}
	}
	if method != http.MethodGet && method != http.MethodHead && personalAccountWrite(bare) {
		return "", NewProblem(http.StatusBadRequest, CodeUnsupportedByCLI,
			"--org does not apply to this command",
			"this command changes your personal account, not the organization "+c.projectOrg+"; run it without --org")
	}
	return path, nil
}

// postgresFlatOnlyDatabaseOps are the /v1/postgres/databases/{id}/<op>
// operations that have no /v1/orgs/{organization}/postgres form yet; they keep
// their flat path under --org (TestOrgRoutePostgresMirrors pins the list).
var postgresFlatOnlyDatabaseOps = map[string]bool{
	"auto-grow": true, "connection-budget": true, "data-api-catalog-preview": true,
	"labels": true, "live": true, "maintenance-window": true, "previews": true,
	"query": true, "roles": true, "status": true, "upgrade": true, "upgrades": true,
}

// postgresFlatOnly reports whether rest (the path below /v1/postgres/) is a
// managed PostgreSQL route with no organization form.
func postgresFlatOnly(rest string) bool {
	parts := strings.Split(rest, "/")
	switch parts[0] {
	case "cutovers", "overage":
		return true
	case "databases":
		return len(parts) >= 3 && postgresFlatOnlyDatabaseOps[parts[2]]
	}
	return false
}

// personalAccountWrite reports whether a write to path changes the caller's
// personal account rather than a company resource.
func personalAccountWrite(path string) bool {
	for _, prefix := range []string{"/v1/account", "/v1/billing", "/v1/keys", "/v1/usage"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}
