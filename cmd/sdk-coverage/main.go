// sdk-coverage is the CI drift gate for the public Go SDK
// (pkg/api.Client). It scans api/openapi.yaml for every documented
// v1 route and walks pkg/api/*.go for every method on *Client, then
// fails when:
//
//   - a route in the spec has no corresponding SDK method (spec → SDK drift).
//   - an SDK method claims a route that's not in the spec (over-spec — usually
//     a renaming mistake; reported as a soft warning so adding new SDK methods
//     ahead of spec work isn't a blocker).
//
// Pure read-only tool: prints a one-line PASS or a numbered list of
// missing methods with the route they belong to, exit 0/1. Designed
// for `make sdk-check` to mirror `make spec-check`'s recipe shape.
//
// Mapping table lives here (not magic) so adding a route is a one-
// line edit. When a route's natural verb clashes with the SDK's name
// (e.g. POST /v1/account/restore ↔ Client.RestoreAccount) the
// explicit table takes precedence over the auto-derivation.
//
// Routes deliberately not exposed in the SDK (anon endpoints, public
// status) are filtered via routeExclude — they're either shape-only
// for documentation or they require authentication schemes the SDK
// doesn't model (HMAC webhooks, session cookies). Future SDK calls
// that add a typed wrapper to those routes are encouraged.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	specRelPath    = "api/openapi.yaml"
	sdkRelPath     = "pkg/api"
	sdkPackageName = "api"
)

// routeExclude lists spec routes that have no SDK method by design.
// Mirror logic in cmd/apid/spec_compliance_test.go::routeExclude;
// keep both in sync.
var routeExclude = map[string]bool{
	"GET /v1/account/dpa":        true, // public markdown (no Bearer; SDK consumers don't render HTML)
	"POST /v1/webhooks/stripe":   true, // HMAC-signed webhook; outside the Bearer-auth surface
	"GET /v1/openapi.yaml":       true, // metadata
	"GET /v1/openapi.json":       true, // metadata
	"POST /v1/cli-auth/code":     true, // anonymous device-code (CLI uses wrapper)
	"POST /v1/cli-auth/exchange": true,
	"GET /status/slo.json":       true,
	"GET /status":                true,

	// Issue #555 trace endpoint. Operator-only surface (X-Faas-Trace-Auth
	// header), not a customer-Bearer-auth endpoint. The SDK does not
	// model the operator token; the route is excluded from the
	// coverage map.
	"GET /v1/traces/{trace_id}": true,

	// Dashboard auth (issue #165 PR #2, ADR-032). The SDK uses the
	// device-code flow for programmatic auth; the dashboard cookie
	// is the browser-only auth artifact. These routes are 302
	// redirects, HTML form posts, or session-cookie endpoints — none
	// of which the Bearer-auth SDK models. The CookieSession header
	// on responses is documented but the SDK's surface is the typed
	// Lgoin/Signup/Reset/SetPassword/Logout wrappers below.
	"GET /v1/auth/google":          true, // 302 to Google consent (browser-only)
	"GET /v1/auth/google/callback": true, // 302 to dashboard (browser-only)
	"GET /v1/auth/github":          true, // 302 to GitHub consent (browser-only)
	"GET /v1/auth/github/callback": true, // 302 to dashboard (browser-only)
	"GET /auth/reset":              true, // HTML form render (browser-only)
	"POST /logout":                 true, // dashboard form post (browser-only); SDK's Logout wraps the same handler as a convenience

	// GitHub install bind picker (PR-B). Browser-only: the dashboard's
	// bind flow drives these via the JS island in the app-detail page
	// using the session cookie established by /v1/auth/github. The
	// Bearer-auth SDK does not model session-cookie POSTs, and the
	// body shape (installation_id + repo_full_name + production_branch)
	// is GitHub-side state — programmatic consumers shouldn't bind
	// apps via API. Mirrors the "browser-only dashboard routes"
	// exclusion above.
	"POST /v1/install/repos/list":       true, // bind picker hydrates from this; browser-only
	"POST /v1/apps/{slug}/install/bind": true, // bind picker writes through this; browser-only
}

// sdkMethodExclude lists methods on *Client that aren't a 1:1 wire
// of any single spec route. Helpers (ListDeploymentsAll) and
// response-shape getters (HTTPClient/BaseURL/Token) belong here so
// the gate doesn't false-positive on them.
var sdkMethodExclude = map[string]bool{
	"HTTPClient":          true,
	"BaseURL":             true,
	"Token":               true,
	"ListDeploymentsAll":  true, // cursor walker; not a route
	"DeployMultipart":     true, // open-ended reader-based upload; CLI's DeployTarball is the wired route
	"MintCliAuthCode":     true, // anonymous device-code mint; route excluded above
	"ExchangeCliAuthCode": true, // anonymous device-code poll; route excluded above
	"GetStatusSLO":        true, // public status; route excluded above
	"Logout":              true, // POST /logout is a browser-form post (excluded above); the SDK wraps the same handler as a convenience
}

// methodRouteMap pins the routes whose natural SDK verb doesn't
// match the standard <Verb><Resource> derivation. Adding a new
// route? Add a row here ONLY if the auto-derivation picks the
// wrong method name; otherwise leave it auto-derived.
//
// Key = "<METHOD> <path>"; value = SDK method name.
var methodRouteMap = map[string]string{
	"DELETE /v1/keys/{id}":                     "DeleteKey",
	"POST /v1/keys/{id}/rotate":                "RotateKey",
	"PATCH /v1/account/keys/grace_window_days": "SetGraceWindow",
	"GET /v1/account/keys/grace_window_days":   "GetGraceWindow",
	"DELETE /v1/domains/{domain}":              "DeleteDomain",
	"DELETE /v1/crons/{id}":                    "DeleteCron",
	"DELETE /v1/apps/{slug}":                   "DeleteApp",
	"DELETE /v1/apps/{slug}/secrets/{key}":     "UnsetSecret",
	"PUT /v1/apps/{slug}/secrets/{key}":        "SetSecret",
	"PATCH /v1/apps/{slug}":                    "UpdateApp",
	"POST /v1/apps/{slug}/rename":              "RenameApp",
	"GET /v1/apps/{slug}":                      "GetApp",
	"GET /v1/apps/{slug}/instances":            "ListInstances",
	"POST /v1/apps/{slug}/park":                "Park",
	"POST /v1/apps/{slug}/wake":                "Wake",
	"POST /v1/apps/{slug}/rollback":            "Rollback",
	"POST /v1/apps/{slug}/deployments":         "Deploy",
	"GET /v1/account/export":                   "ExportAccount",
	"DELETE /v1/account":                       "DeleteAccount",
	"PATCH /v1/account/plan":                   "ChangePlan",
	"GET /v1/account":                          "Whoami",
	"POST /v1/account/restore":                 "RestoreAccount",
	"GET /v1/apps/{slug}/logs":                 "StreamAppLogs",
	"GET /v1/deployments/{id}/logs":            "StreamDeploymentLogs",
	"GET /v1/deployments/{id}":                 "GetDeployment",
	"GET /v1/deployments":                      "ListDeployments",
	"GET /v1/apps":                             "ListApps",
	"POST /v1/apps":                            "CreateApp",
	"GET /status/slo.json":                     "GetStatusSLO",
	"PATCH /v1/crons/{id}":                     "UpdateCron",
	"POST /v1/crons":                           "CreateCron",
	"GET /v1/crons":                            "ListCrons",
	"GET /v1/usage/summary":                    "UsageSummary",
	"GET /v1/usage":                            "GetUsage",
	"GET /v1/usage/daily":                      "UsageDaily",
	"GET /v1/usage/storage":                    "StorageUsage",
	"GET /v1/invoices":                         "ListInvoices",
	"GET /v1/apps/{slug}/secrets":              "ListSecrets",
	"GET /v1/domains":                          "ListDomains",
	"POST /v1/domains":                         "CreateDomain",

	// Issue #396 / ADR-045 PR 3 — alert rules. The auto-derivation
	// would produce names with literal hyphens for the rotate-secret
	// action ("PostAppsSlugAlertsIdRotate-secret"); the SDK verb is
	// "RotateAlertRuleSecret" so the explicit map drops the hyphen.
	// The other 5 entries exist because the SDK names them after the
	// resource noun (AlertRule) rather than the path placeholder
	// concatenation (AppsSlugAlerts) — same convention as crons.
	"GET /v1/apps/{slug}/alerts":                     "ListAlertRules",
	"POST /v1/apps/{slug}/alerts":                    "CreateAlertRule",
	"GET /v1/apps/{slug}/alerts/{id}":                "GetAlertRule",
	"PATCH /v1/apps/{slug}/alerts/{id}":              "UpdateAlertRule",
	"DELETE /v1/apps/{slug}/alerts/{id}":             "DeleteAlertRule",
	"POST /v1/apps/{slug}/alerts/{id}/rotate-secret": "RotateAlertRuleSecret",
	"GET /v1/keys":                                   "ListKeys",
	"POST /v1/keys":                                  "CreateKey",
	// Move 2 routes — the auto-derivation produces names with literal
	// hyphens (e.g. "DeleteDelayed-tasksId") because the spec path uses
	// the k8s-style hyphen; the explicit map below drops the hyphen and
	// conforms to the SDK's flat resource naming.
	"POST /v1/apps/{slug}/invoke":            "InvokeApp",
	"POST /v1/apps/{slug}/invoke/async":      "InvokeAppAsync",
	"POST /v1/apps/{slug}/queues/send":       "QueueSend",
	"POST /v1/apps/{slug}/queues/receive":    "QueueReceive",
	"POST /v1/apps/{slug}/queues/{id}/ack":   "AckQueueRow",
	"GET /v1/apps/{slug}/queues/state":       "QueueState",
	"GET /v1/apps/{slug}/queues/peek":        "QueuePeek",
	"GET /v1/apps/{slug}/queues/dead_letter": "QueueDeadLetter",
	"POST /v1/apps/{slug}/delayed-tasks":     "CreateDelayedTask",
	"GET /v1/delayed-tasks/{id}":             "GetDelayedTask",
	"DELETE /v1/delayed-tasks/{id}":          "CancelDelayedTask",
	"GET /v1/invocations":                    "ListInvocations",
	"GET /v1/invocations/{id}":               "GetInvocation",
	// Issue #279 — operator credits. The auto-derivation produces
	// "PostAdminAccountsIdCredits" which reads as a Swagger-style
	// artifact; the SDK verb is "issue" (the operator's mental
	// model) so the explicit map takes precedence.
	"POST /v1/admin/accounts/{id}/credits": "IssueAccountCredit",
	// Issue #279 PR-C — credit consumption reducer. Auto-derivation
	// produces "PostInvoicesIdConsume-credits" (literal hyphen); the
	// SDK verb is "ConsumeInvoiceCredits" so the explicit map drops
	// the hyphen.
	"POST /v1/invoices/{id}/consume-credits": "ConsumeInvoiceCredits",

	// IAM-4 (ADR-035) audit log surface. The auto-derivation would
	// otherwise produce names with literal hyphens ("GetAudit-events",
	// "GetAudit-eventsId") because the spec path uses an unhyphenated
	// root resource; the explicit map drops the hyphen and conforms to
	// the SDK's flat resource naming.
	"GET /v1/audit-events":      "ListAuditEvents",
	"GET /v1/audit-events/{id}": "GetAuditEvent",

	// Issue #517 / PR-C / ADR-064 — wake timeline. The route is a
	// sub-resource of /v1/apps/{slug}/wakes/{wake_id}/timeline; the
	// auto-derivation would produce "GetAppsSlugWakesWake-idTimeline"
	// (literal hyphens preserved in the path segment). The explicit
	// map drops the path-separator noise and conforms to the SDK's
	// flat verb naming.
	"GET /v1/apps/{slug}/wakes/{wake_id}/timeline": "ListWakeTimeline",

	// ADR-050 Phase 3 — repo decomposition. The two routes take
	// multipart bodies so the SDK verb is named after the action
	// (scan / apply) rather than the resource noun. The auto-
	// derivation would produce "PostProjects" for both, which is
	// indistinguishable in the SDK. Apply must hit POST /v1/projects
	// (not /v1/projects/scan) — the SDK enforces this in
	// ApplyProjectPlan via url.QueryEscape(plan_token).
	"POST /v1/projects/scan": "ScanProject",
	"POST /v1/projects":      "ApplyProjectPlan",

	// Issue #273 / ADR-042 — per-app metrics. The auto-derivation
	// would produce GetAppsSlugMetrics (Swagger-style); the SDK
	// names it GetAppMetrics to match the existing per-app methods
	// (GetApp, ListApps) — drop the slug placeholder from the verb.
	"GET /v1/apps/{slug}/metrics": "GetAppMetrics",

	// Issue #393 — account-scoped list endpoints. Distinct from the
	// per-app counterparts (ListInstances / ListSecrets / GetAppMetrics)
	// which take a slug; the aggregate route has no slug, so the SDK
	// uses Get<Resource> to flag the account-scoped contract (one call
	// replaces N per-app calls — see ADR-045).
	"GET /v1/instances":    "GetInstances",
	"GET /v1/secrets":      "GetSecrets",
	"GET /v1/apps/metrics": "GetAppsMetrics",

	// Dashboard auth (issue #165 PR #2, ADR-032). The auto-derivation
	// picks Verb+Resource (e.g. "PostLogin" for POST /login) but the
	// SDK named these methods deliberately after the user-facing action
	// (PasswordLogin vs PostLogin, etc.) — pin them so the gate stays
	// the spec's source of truth.
	"POST /login":                          "PasswordLogin",
	"POST /signup":                         "PasswordSignup",
	"POST /login/forgot":                   "RequestPasswordReset",
	"POST /auth/reset":                     "ConfirmPasswordReset",
	"POST /dashboard/account/set-password": "SetPassword",

	// IAM-3 (ADR-039) dashboard session surface. The auto-derivation
	// strips /v1/ and title-cases each segment, so POST /v1/auth/logout
	// becomes "PostAuthLogout". The SDK named these methods after
	// the account-scoped noun (Post*Account*) to mirror the
	// Get/Post/Patch/Delete pattern used elsewhere; pin them.
	"POST /v1/auth/logout":              "PostAccountLogout",
	"GET /v1/auth/sessions":             "GetAccountSessions",
	"DELETE /v1/auth/sessions/{id}":     "DeleteAccountSession",
	"POST /v1/auth/sessions/revoke_all": "PostAccountSessionsRevokeAll",

	// Issue #472 / ADR-054 — cosign signature-enforcement surface. The
	// auto-derivation would produce names with literal underscores
	// ("GetAppsSlugTrusted_signers", "DeleteAppsSlugTrusted_signersName",
	// "PutAppsSlugTrusted_signersName") because the spec path uses an
	// underscore; the SDK verb drops the placeholder + underscore
	// segment and conforms to the flat resource naming (same pattern
	// as alerts). PATCH /v1/apps/{slug}/security would auto-derive to
	// "PatchAppsSlugSecurity" — pin it explicitly so the gate stays
	// the SDK's source of truth on verb choice.
	"GET /v1/apps/{slug}/trusted_signers":           "ListAppTrustedSigners",
	"PUT /v1/apps/{slug}/trusted_signers/{name}":    "PutAppTrustedSigner",
	"DELETE /v1/apps/{slug}/trusted_signers/{name}": "DeleteAppTrustedSigner",
	"PATCH /v1/apps/{slug}/security":                "UpdateAppSecurity",
	// Per-app private-registry Basic Auth (issue #461 / ADR-062). The
	// SDK natural verb auto-derives to "GetAppsSlugRegistry-credentials"
	// (dash, not the safer "RegistryCredentials") because the spec path
	// uses a hyphen. Pin the verb here so the gate stays the SDK's
	// source of truth on verb choice, matching the trusted_signers
	// pattern above.
	"GET /v1/apps/{slug}/registry-credentials":    "ListAppRegistryCredentials",
	"PUT /v1/apps/{slug}/registry-credentials":    "SetAppRegistryCredential",
	"DELETE /v1/apps/{slug}/registry-credentials": "DeleteAppRegistryCredential",
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "sdk-coverage: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := findRepoRoot()
	if err != nil {
		return err
	}
	spec, err := loadSpec(filepath.Join(root, specRelPath))
	if err != nil {
		return err
	}
	methods, err := loadClientMethods(filepath.Join(root, sdkRelPath))
	if err != nil {
		return err
	}
	report := analyze(spec, methods)
	report.print()
	if !report.ok() {
		os.Exit(1)
	}
	return nil
}

// report collects the drift findings.
type reportT struct {
	missing []string // spec routes without an SDK method
	warn    []string // SDK methods without a spec route (soft)
}

func (r reportT) ok() bool { return len(r.missing) == 0 }
func (r *reportT) print() {
	if len(r.missing) == 0 && len(r.warn) == 0 {
		fmt.Println("sdk-coverage: PASS — every spec route has a typed SDK method")
		return
	}
	if len(r.missing) > 0 {
		fmt.Println("sdk-coverage: FAIL — these spec routes have no SDK method:")
		for _, m := range r.missing {
			fmt.Printf("  - %s\n", m)
		}
	}
	if len(r.warn) > 0 {
		fmt.Println("sdk-coverage: warn — these SDK methods have no matching spec route (usually helpers like List*All):")
		for _, w := range r.warn {
			fmt.Printf("  - %s\n", w)
		}
	}
}

func analyze(spec map[string]map[string]any, methods map[string]bool) reportT {
	r := reportT{}
	methodUsage := map[string]bool{} // SDK methods touched by a route
	for path, ops := range spec {
		for method := range ops {
			key := strings.ToUpper(method) + " " + path
			if routeExclude[key] {
				continue
			}
			sdkName := methodRouteMap[key]
			if sdkName == "" {
				sdkName = deriveMethodName(method, path)
			}
			if !methods[sdkName] {
				r.missing = append(r.missing, fmt.Sprintf("%s → SDK method %q", key, sdkName))
			}
			methodUsage[sdkName] = true
		}
	}
	for m := range methods {
		if sdkMethodExclude[m] || methodUsage[m] {
			continue
		}
		r.warn = append(r.warn, m+" (no spec route)")
	}
	sort.Strings(r.missing)
	sort.Strings(r.warn)
	return r
}

// deriveMethodName produces a best-effort SDK method name from a
// path + method. Falls back to "<Method>_<path-sanitized>" so the
// failure message is descriptive even when the auto-derivation
// misses the mark. The map in methodRouteMap overrides this when
// the natural verb differs (e.g. POST …/deployments → Deploy).
//
// Today every spec route is in methodRouteMap, so this function is
// unreachable at runtime. It exists only as a fallback so a future
// route that ships without a map entry produces a descriptive error
// (the developer sees "POST /v1/apps/{slug}/x → SDK method PostAppsSlugX"
// instead of "unknown"). Not authoritative; methodRouteMap is.
func deriveMethodName(method, path string) string {
	method = titleCase(strings.ToLower(method))
	// Strip /v1/ prefix and {} placeholders; title-case each segment.
	cleaned := strings.TrimPrefix(path, "/v1/")
	cleaned = strings.ReplaceAll(cleaned, "{", "")
	cleaned = strings.ReplaceAll(cleaned, "}", "")
	segments := strings.Split(cleaned, "/")
	for i, s := range segments {
		segments[i] = titleCase(s)
	}
	res := strings.Join(segments, "")
	return method + res
}

// titleCase upper-cases the first byte and leaves the rest unchanged.
// Avoids importing golang.org/x/text/cases for a fallback-only helper
// that the drift gate never reaches at runtime (every spec route is
// in methodRouteMap).
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < 16; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
	return "", fmt.Errorf("go.mod not found (walked up 16 levels)")
}

// loadSpec reads api/openapi.yaml and returns path -> method -> true.
// Same string-keyed view as cmd/apid/spec_compliance_test.go.
func loadSpec(path string) (map[string]map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	root := yaml.Node{}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, fmt.Errorf("unexpected spec yaml shape")
	}
	spec := root.Content[0]
	// OpenAPI is a flat mapping; walk top-level Content (alternating
	// key/value nodes) rather than depending on a ContentMap helper.
	var paths *yaml.Node
	for i := 0; i+1 < len(spec.Content); i += 2 {
		if spec.Content[i].Value == "paths" {
			paths = spec.Content[i+1]
			break
		}
	}
	if paths == nil {
		return out, nil
	}
	for i := 0; i+1 < len(paths.Content); i += 2 {
		pn := paths.Content[i]   // path key
		on := paths.Content[i+1] // operation mapping
		if on.Kind != yaml.MappingNode {
			continue
		}
		methods := map[string]any{}
		for j := 0; j+1 < len(on.Content); j += 2 {
			m := on.Content[j].Value
			switch m {
			case "get", "post", "put", "patch", "delete":
				methods[m] = true
			}
		}
		if len(methods) > 0 {
			out[pn.Value] = methods
		}
	}
	return out, nil
}

// loadClientMethods walks pkg/api/*.go and returns the set of method
// names declared on *Client (the public SDK surface).
func loadClientMethods(dir string) (map[string]bool, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(os.FileInfo) bool { return true }, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	pkg, ok := pkgs[sdkPackageName]
	if !ok {
		return nil, fmt.Errorf("package %q not found in %s (found %d packages)", sdkPackageName, dir, len(pkgs))
	}
	out := map[string]bool{}
	for _, file := range pkg.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || !fd.Name.IsExported() {
				return true
			}
			// Filter to methods on *Client only.
			if !isClientRecv(fd.Recv) {
				return true
			}
			out[fd.Name.Name] = true
			return true
		})
	}
	return out, nil
}

func isClientRecv(recv *ast.FieldList) bool {
	if recv == nil || len(recv.List) == 0 {
		return false
	}
	t, ok := recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := t.X.(*ast.Ident)
	if !ok {
		return false
	}
	return id.Name == "Client"
}
