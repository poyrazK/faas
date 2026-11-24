package claim

import (
	"encoding/json"
	"sort"
	"strings"
)

// Route policy for an unclaimed account (ADR-20261124080000): it may read
// under the deploy surfaces and make only the writes a deploy needs. The
// writes are listed route by route, so a new route under /v1/apps is closed
// to unclaimed accounts until someone decides otherwise: a block list by
// path segment let credentials (AI-gateway tokens, realtime publish keys),
// log drains and environment clones through.

// readRoots are the API roots an unclaimed account may read.
var readRoots = []string{
	"/v1/apps", "/v1/deployments", "/v1/uploads", "/v1/projects", "/v1/builds",
	"/v1/account", "/v1/me", "/v1/whoami", "/v1/usage", "/v1/runtimes",
}

// writeRoutes are the exact route patterns (method and path, as registered)
// an unclaimed account may call to write. TestWriteRoutesAreRegistered keeps
// them in step with apid's route table.
var writeRoutes = map[string]bool{
	// Create, deploy and remove apps.
	"POST /v1/apps":                                   true,
	"DELETE /v1/apps/{slug}":                          true,
	"POST /v1/apps/{slug}/deployments":                true,
	"POST /v1/apps/{slug}/deployments/source-tarball": true,
	"POST /v1/apps/{slug}/deployments/{id}/cancel":    true,
	"POST /v1/apps/{slug}/deployment-staging":         true,
	"POST /v1/apps/{slug}/rollback":                   true,
	"POST /v1/apps/{slug}/restart":                    true,
	"DELETE /v1/deployments/{id}":                     true,
	"POST /v1/deployments/{id}/retry":                 true,
	// Source uploads.
	"POST /v1/uploads":             true,
	"PATCH /v1/uploads/{id}":       true,
	"POST /v1/uploads/{id}/commit": true,
	"DELETE /v1/uploads/{id}":      true,
	// The app's own configuration.
	"PUT /v1/apps/{slug}/env/{key}":        true,
	"DELETE /v1/apps/{slug}/env/{key}":     true,
	"POST /v1/apps/{slug}/env/batch":       true,
	"PUT /v1/apps/{slug}/secrets/{key}":    true,
	"DELETE /v1/apps/{slug}/secrets/{key}": true,
	// A fresh claim link.
	"POST /v1/account/claim-link": true,
}

// fieldGatedRoutes are writes an unclaimed account may make with only the
// listed top-level body fields; RefusedField names any other. App settings
// a deploy applies (public auth, protocol, size, restart behaviour) pass;
// egress, service bindings, minimum instances and the rest wait for a claim.
// Project deploys are not here: they create databases and several apps, so
// they need a claimed account.
var fieldGatedRoutes = map[string]map[string]bool{
	"PATCH /v1/apps/{slug}": {
		"require_authn": true, "public_auth": true, "app_protocol": true,
		"platform_tenant_required": true, "resource_profile": true,
		"ram_mb": true, "cpu_millicores": true, "idle_timeout_s": true,
		"max_concurrency": true, "execution_mode": true, "restart_policy": true,
		"startup_deadline_s": true, "max_retries": true, "service_replicas": true,
		"stop_grace_period_s": true, "stop_signal": true, "request_timeout_s": true,
		"health_path": true,
	},
}

// FieldGated reports whether the route is allowed only with some body
// fields; the caller passes the body to RefusedField.
func FieldGated(pattern string) bool { return fieldGatedRoutes[pattern] != nil }

// RefusedField returns the first body field an unclaimed account may not
// send to the field-gated route, "body" when the body is not a JSON object,
// or "" when every field is allowed.
func RefusedField(pattern string, body []byte) string {
	allowed := fieldGatedRoutes[pattern]
	if allowed == nil {
		return ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return "body"
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		if !allowed[name] {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return names[0]
}

// RouteAllowed reports whether an unclaimed account may call the route
// registered as pattern ("METHOD /path/{param}", http.Request.Pattern).
func RouteAllowed(pattern string) bool {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok {
		return false
	}
	switch method {
	case "GET", "HEAD":
		for _, root := range readRoots {
			if path == root || strings.HasPrefix(path, root+"/") || strings.HasPrefix(path, root+":") {
				return true
			}
		}
		return false
	}
	return writeRoutes[pattern] || FieldGated(pattern)
}

// WriteRoutes returns the writable route patterns, for tests.
func WriteRoutes() []string {
	out := make([]string, 0, len(writeRoutes)+len(fieldGatedRoutes))
	for p := range writeRoutes {
		out = append(out, p)
	}
	for p := range fieldGatedRoutes {
		out = append(out, p)
	}
	return out
}
