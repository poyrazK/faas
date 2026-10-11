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
