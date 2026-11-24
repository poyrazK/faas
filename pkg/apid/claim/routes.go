package claim

import (
	"encoding/json"
	"net/http"
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

// readDenied are GET routes under the read roots that act rather than
// read: sessions that drive app state. WebSocket upgrades are refused as a
// class by the middleware (IsUpgrade); these are named as well, so the
// policy holds whatever the client sends.
var readDenied = map[string]bool{
	"GET /v1/apps/{slug}/entities/connect":           true,
	"GET /v1/apps/{slug}/entities/session-connect":   true,
	"GET /v1/apps/{slug}/forks/{id}/execs":           true,
	"GET /v1/apps/{slug}/forks/{id}/execs/{exec_id}": true,
}

// credentialSegments mark reads of keys, tokens and credentials under the
// read roots. An unclaimed account cannot create any, and listing them is
// refused as a class, like GET /v1/keys, so a new credential route is
// closed too.
var credentialSegments = map[string]bool{
	"keys": true, "access-tokens": true, "ai-access-tokens": true, "deploy-tokens": true,
	"credentials": true, "s3-credentials": true, "registry-credentials": true,
	"publish-credentials": true, "credential-policy": true, "access-grants": true,
	"trusted_signers": true, "platform-tenants": true,
}

// readsCredentials reports a path with a credential segment.
func readsCredentials(path string) bool {
	for seg := range strings.SplitSeq(path, "/") {
		if credentialSegments[seg] {
			return true
		}
	}
	return false
}

// IsUpgrade reports a request asking to switch protocols (a WebSocket).
// An unclaimed account's reads are plain HTTP; an upgraded session acts.
func IsUpgrade(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("Upgrade")) != ""
}

// writeRoutes are the exact route patterns (method and path, as registered)
// an unclaimed account may call to write. TestWriteRoutesAreRegistered keeps
// them in step with apid's route table.
var writeRoutes = map[string]bool{
	// Deploy and remove apps (creating one is field-gated below).
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

// appSettings are the app settings a deploy applies, which an unclaimed
// account may set: public access, protocol, size, timeouts, replicas and
// restart behaviour. Egress, service bindings, warm pools, minimum
// instances, extra ports and the rest wait for a claim.
var appSettings = []string{
	"require_authn", "public_auth", "app_protocol", "platform_tenant_required",
	"resource_profile", "ram_mb", "cpu_millicores", "idle_timeout_s",
	"max_concurrency", "execution_mode", "restart_policy", "startup_deadline_s",
	"max_retries", "service_replicas", "stop_grace_period_s", "stop_signal",
	"request_timeout_s", "health_path",
}

// fieldGatedRoutes are writes an unclaimed account may make with only the
// listed top-level body fields; RefusedField names any other. Creating an
// app takes the same settings as changing one, plus what names its shape.
// Project deploys are not here: they create databases and several apps, so
// they need a claimed account.
var fieldGatedRoutes = map[string]map[string]bool{
	"POST /v1/apps":         fieldSet(append([]string{"slug", "type", "runtime", "vcpu"}, appSettings...)...),
	"PATCH /v1/apps/{slug}": fieldSet(appSettings...),
}

func fieldSet(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
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
		if readDenied["GET "+path] || readsCredentials(path) {
			return false
		}
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
