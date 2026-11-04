package reqbudget

import (
	"net/http"
	"strings"
)

// IsSyncInvokeRequest identifies the customer-facing synchronous invoke
// endpoint. It is deliberately exact: the async sibling must keep the normal
// short edge budget because it returns immediately with 202 Accepted.
func IsSyncInvokeRequest(r *http.Request) bool {
	if r == nil || r.Method != http.MethodPost {
		return false
	}
	const prefix = "/v1/apps/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	appAndSuffix := strings.TrimPrefix(r.URL.Path, prefix)
	if app, suffix, ok := strings.Cut(appAndSuffix, "/entities/invoke"); ok && app != "" && !strings.Contains(app, "/") && suffix == "" {
		return true
	}
	app, suffix, ok := strings.Cut(appAndSuffix, "/invoke")
	return ok && app != "" && !strings.Contains(app, "/") && suffix == ""
}

// IsDeploymentResponseCheckRequest identifies the bounded owner check that
// can wait for a selected deployment to wake. Sibling reads keep short budgets.
func IsDeploymentResponseCheckRequest(r *http.Request) bool {
	return r != nil && r.Method == http.MethodPost && pathMatches("/v1/apps/*/deployments/*/response-check", r.URL.Path)
}

// deploySubmissionRoutes are the apid routes that receive deploy source or
// submit a deployment. A commit unpacks and scans the whole source archive
// several times before it records the deployment; on production-us that
// regularly outran the 5 s apid default, and every server-side deploy
// failure in three days was its deadline firing mid-request (hunt #9,
// H9-157; ADR-093 amendment). Project apply runs under its own timeout.
// "*" matches one non-empty path segment.
var deploySubmissionRoutes = []struct{ method, path string }{
	{http.MethodPatch, "/v1/uploads/*"},
	{http.MethodPost, "/v1/uploads/*/commit"},
	{http.MethodPost, "/v1/apps/*/deployments"},
	{http.MethodPost, "/v1/apps/*/deployments/source-tarball"},
	{http.MethodPost, "/v1/apps/*/deployments/source-ref"},
	{http.MethodPost, "/v1/apps/*/rollback"},
	{http.MethodPost, "/v1/deployments/*/retry"},
}

// IsDeploySubmissionRequest identifies a request that uploads deploy source
// or submits a deployment.
func IsDeploySubmissionRequest(r *http.Request) bool {
	return matchesRoute(r, deploySubmissionRoutes)
}

// accountExportRoutes assemble the whole account (every app, deployment and
// audit row) into one spool before the first byte goes out. On
// production-us a 44-app account outran the 5 s apid default on every
// attempt and got 503 "could not assemble export" (hunt #9 H9-136; again on
// rc.304 and rc.305, Hunt 14 H14-120).
var accountExportRoutes = []struct{ method, path string }{
	{http.MethodGet, "/v1/account/export"},
	{http.MethodGet, "/dashboard/account/export"},
}

// IsAccountExportRequest identifies the REST and dashboard account exports.
func IsAccountExportRequest(r *http.Request) bool {
	return matchesRoute(r, accountExportRoutes)
}

func matchesRoute(r *http.Request, routes []struct{ method, path string }) bool {
	if r == nil {
		return false
	}
	for _, route := range routes {
		if r.Method == route.method && pathMatches(route.path, r.URL.Path) {
			return true
		}
	}
	return false
}

// longWaitRoutes are the apid routes that hold a request past the 5 s
// default by design: queue lease long-polls of up to 20 s (ADR-1140), the
// policy status wait of up to 10 s, and work that grows with the source (a
// project scan of a repository tarball, a dev source upload). Under the
// default every one answered 504 at 5 s; an SDK consumer leasing from an idle
// queue got an error and backed off instead of an empty list (bug hunt
// 2026-10-28 B35). A route that needs longer than the platform ceiling must
// detach its work instead.
//
// The second group set their own longer timeouts (10 s to 2 minutes) that the
// 5 s default silently cut: alert test sends, invoice history pages and
// refreshes, profile queries, the app health read, the Slack and Linear OAuth
// exchanges, binding promotion, MCP readiness and the native issue-tracker
// calls. They get the ceiling too; a test send or invoice page that needs
// longer answers before the ceiling instead of being cut at 5 s.
var longWaitRoutes = []struct{ method, path string }{
	{http.MethodPost, "/v1/apps/*/queues/*/leases"},
	{http.MethodPost, "/v1/orgs/*/apps/*/queues/*/leases"},
	{http.MethodGet, "/v1/apps/*/policy/status"},
	{http.MethodPost, "/v1/projects/scan/source-ref"},
	{http.MethodPost, "/v1/apps/*/deployments/dev-source"},

	{http.MethodPost, "/v1/notification-channels/*/test"},
	{http.MethodPost, "/v1/apps/*/alert-presets/*/test"},
	{http.MethodPost, "/dashboard/apps/*/alert-presets/*/test"},
	{http.MethodPost, "/v1/invoices/backfill"},
	{http.MethodPost, "/v1/invoices/*/refresh"},
	{http.MethodGet, "/v1/apps/*/profiles"},
	{http.MethodGet, "/v1/apps/*/profiles/heap"},
	{http.MethodPost, "/v1/apps/*/profiles/compare"},
	{http.MethodPost, "/v1/apps/*/profiles/investigations/*/check"},
	{http.MethodGet, "/v1/apps/*/health"},
	{http.MethodGet, "/oauth/slack/callback"},
	{http.MethodGet, "/oauth/linear/callback"},
	{http.MethodPost, "/v1/deployments/*/promote"},
	{http.MethodPost, "/v1/deployments/*/promote-with-application-ack"},
	{http.MethodPost, "/v1/deployments/*/mcp-readiness"},
	{http.MethodPost, "/dashboard/mcp/apps/*/oauth"},
	{http.MethodPost, "/v1/apps/*/issues/*/native-tickets"},
	{http.MethodPost, "/v1/apps/*/issues/*/native-linear-tickets"},
	{http.MethodGet, "/v1/apps/*/issues/*/tracker-links/*/native-status"},
	{http.MethodPost, "/v1/apps/*/issues/*/tracker-links/*/native-status"},
	{http.MethodGet, "/v1/apps/*/issues/*/tracker-links/*/native-linear-status"},
	{http.MethodPost, "/v1/apps/*/issues/*/tracker-links/*/native-linear-status"},
	{http.MethodGet, "/v1/apps/*/issues/*/tracker-links/*/auto-sync"},
	{http.MethodPut, "/v1/apps/*/issues/*/tracker-links/*/auto-sync"},
	{http.MethodGet, "/v1/apps/*/issue-tracker-linear-connection"},
	{http.MethodPut, "/v1/apps/*/issue-tracker-linear-connection"},
	{http.MethodPost, "/dashboard/apps/*/issues/*/tracker-sync"},
	{http.MethodPost, "/dashboard/apps/*/issues/*/linear-connection"},
	{http.MethodGet, "/dashboard/apps/*/issues/*/tracker-sync/*"},
	{http.MethodGet, "/dashboard/apps/*/issues/slack-routing"},
}

// IsLongWaitRequest identifies a request on one of longWaitRoutes.
func IsLongWaitRequest(r *http.Request) bool {
	return matchesRoute(r, longWaitRoutes)
}

func pathMatches(pattern, path string) bool {
	want, got := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] == "*" && got[i] != "" {
			continue
		}
		if want[i] != got[i] {
			return false
		}
	}
	return true
}
