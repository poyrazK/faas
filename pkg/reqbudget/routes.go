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
	if r == nil {
		return false
	}
	for _, route := range deploySubmissionRoutes {
		if r.Method == route.method && pathMatches(route.path, r.URL.Path) {
			return true
		}
	}
	return false
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
