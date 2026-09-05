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
	"GET /v1/account/dpa":                       true, // public markdown (no Bearer; SDK consumers don't render HTML)
	"POST /v1/webhooks/stripe":                  true, // HMAC-signed webhook; outside the Bearer-auth surface
	"POST /v1/webhooks/resend":                  true, // Svix-signed webhook (issue #246 / ADR-115); outside the Bearer-auth surface
	"GET /v1/openapi.yaml":                      true, // metadata
	"GET /v1/openapi.json":                      true, // metadata
	"GET /v1/internal/metrics/targets":          true, // issue #1219 — loopback Prometheus HTTP-SD endpoint
	"GET /v1/internal/metrics/promtail-targets": true, // issue #274 — loopback Promtail HTTP-SD endpoint
	"POST /v1/cli-auth/code":                    true, // anonymous device-code (CLI uses wrapper)
	"POST /v1/cli-auth/exchange":                true,
	"GET /status/slo.json":                      true,
	"GET /status":                               true,

	// Issue #555 trace endpoint. Operator-only surface (X-Faas-Trace-Auth
	// header), not a customer-Bearer-auth endpoint. The SDK does not
	// model the operator token; the route is excluded from the
	// coverage map.
	"GET /v1/traces/{trace_id}": true,

	// Issue #777 / ADR-091: operator observability backend. Same
	// operator-only posture as /v1/compute-nodes — admin scope
	// + FAAS_ADMIN_EMAILS allowlist. Mirror the exclusion across
	// BOTH this list AND cmd/apid/spec_compliance_test.go::routeExclude;
	// the two lists must move together.
	"GET /v1/admin/obs/overview":                true, // ADR-091 — operator-only
	"GET /v1/admin/obs/capacity":                true, // operator-only capacity projection
	"GET /v1/admin/obs/tenants":                 true, // ADR-091 — operator-only
	"GET /v1/admin/obs/tenants/{id}/360":        true, // operator-only tenant 360 projection
	"GET /v1/admin/obs/tenants/{id}":            true, // ADR-091 — operator-only
	"GET /v1/admin/obs/tenants/{id}/activity":   true, // operator-only tenant activity drill-down
	"GET /v1/admin/obs/apps/{id}":               true, // operator-only app workload drill-down
	"GET /v1/admin/obs/nodes":                   true, // ADR-091 — operator-only
	"GET /v1/admin/obs/nodes/{name}/detail":     true, // operator-only node workload drill-down
	"GET /v1/admin/obs/nodes/{name}/heartbeats": true, // ADR-091 — operator-only
	"GET /v1/admin/obs/anomalies":               true, // ADR-091 — operator-only (PR #2)
	"GET /v1/admin/obs/rate-limits":             true, // ADR-091 — operator-only (PR #2)
	"GET /v1/admin/obs/audit-log/search":        true, // ADR-091 — operator-only (PR #3)
	"GET /v1/admin/obs/events":                  true, // ADR-091 — operator-only (PR #3)
	"GET /v1/admin/obs/nodes/events":            true, // ADR-091 — operator-only SSE (PR #3; successor to /v1/compute-nodes/events)
	"GET /v1/admin/obs/nodes/wake-latency":      true, // ADR-092 — operator-only per-node wake-latency quantiles (PR #4)
	"GET /v1/admin/obs/builder-heartbeats":      true, // ADR-091 — operator-only (operator-side mega-PR Commit 7 / P5)
	"GET /v1/admin/obs/health":                  true, // Obs-Meta + Trace-IDs Mega-PR / C7 — operator-only meta-obs health snapshot

	// Operator-side observability mega-PR (PR #1099) P2 recovery
	// primitives. Same operator-only posture as the ADR-091
	// cluster above: admin scope + FAAS_ADMIN_EMAILS allowlist;
	// SDK consumers authenticate as a tenant, not an operator.
	// Mirror the exclusion across BOTH this list AND
	// cmd/apid/spec_compliance_test.go::routeExclude; the two
	// lists must move together.
	"POST /v1/admin/instances/{id}/force-park":         true, // PR #1099 P2a — operator-only recovery primitive
	"POST /v1/admin/apps/{slug}/force-cold-boot":       true, // PR #1099 P2b — operator-only recovery primitive
	"POST /v1/admin/instances/{id}/force-restart":      true, // PR #1105 P2d — operator-only recovery primitive
	"POST /v1/admin/builds/sweep-stuck":                true, // PR #1099 P2c — operator-only recovery primitive
	"POST /v1/admin/ops/accounts/{id}/suspend":         true, // operator-only tenant lifecycle control
	"POST /v1/admin/ops/accounts/{id}/restore":         true, // operator-only tenant lifecycle control
	"POST /v1/admin/ops/accounts/{id}/revoke-sessions": true, // operator-only tenant security control
	"POST /v1/admin/ops/nodes/{name}/drain":            true, // operator-only node lifecycle control
	"POST /v1/admin/ops/nodes/{name}/force-drain":      true, // operator-only destructive node lifecycle control
	"POST /v1/admin/ops/nodes/{name}/activate":         true, // operator-only node lifecycle control
	"GET /v1/admin/operator-intents/{id}":              true, // PR #1099 P2.3 — operator-only intent polling endpoint
	"GET /v1/admin/config":                             true, // operator-only runtime configuration catalog
	"PATCH /v1/admin/config/{key}":                     true, // operator-only runtime configuration write
	"GET /v1/admin/config-operations/{id}":             true, // operator-only runtime configuration operation polling
	"GET /v1/admin/config/{key}/revisions":             true, // operator-only runtime configuration history

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
	"GET /v1/auth/verify-email":    true, // HTML email-confirmation landing page (browser-only)
	"GET /auth/reset":              true, // HTML form render (browser-only)
	"POST /logout":                 true, // dashboard form post (browser-only); SDK's Logout wraps the same handler as a convenience

	// PR #722 review (cookie-only routes CLI rejects). The /v1/auth/sessions
	// handlers read sessionFrom(r), which is cookie-only per
	// pkg/auth/middleware/context.go:141; the SDK cannot model a
	// bearer-key caller and the dashboard is the supported surface.
	// /v1/auth/capabilities is mounted behind sessionAuth at
	// server.go:1085 — same exclusion.
	"GET /v1/auth/csrf":                 true, // browser-only CSRF envelope; the public SDK uses bearer auth
	"GET /v1/auth/sessions":             true, // sessionFrom cookie-only; PR #722 dropped
	"DELETE /v1/auth/sessions/{id}":     true, // same + CSRF cookie required
	"POST /v1/auth/sessions/revoke_all": true, // same — fails 401 on bearer-key
	"GET /v1/auth/capabilities":         true, // sessionAuth at server.go:1085

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

	// Issue #961 / Mega-B PR-3 / ADR-116. The dashboard's
	// /dashboard/apps/new wizard renders GET /v1/templates as the
	// "Starting template" dropdown. Cookie-session-authenticated
	// (NOT API-key) — the dashboard is the install-token trust root
	// per ADR-116. The Bearer-auth SDK does not model session-cookie
	// GETs, and the response shape is the same embed.FS the CLI
	// reads locally (cmd/gregale/templates.Names) so SDK consumers
	// don't need a typed wrapper. Mirrors the dashboard-auth
	// exclusion above.
	"GET /v1/templates": true, // dashboard wizard hydrates from this; session-cookie-only

	// ADR-123 — alert-preset catalog dashboard form post. The
	// dashboard's preset-enable card on app_detail.html renders
	// `action="/apps/{slug}/alert-presets/{name}/enable"` and posts
	// x-www-form-urlencoded body to /dashboard/apps/{slug}/alert-
	// presets/{name}/enable. The SDK does not model the
	// session-cookie-only dashboard auth surface (mirrors /logout
	// above) — programatic enablement goes through
	// POST /v1/apps/{slug}/alert-presets/{name}/enable which the
	// SDK exposes as EnableAlertPreset.
	"POST /dashboard/apps/{slug}/alert-presets/{name}/enable": true,
	// ADR-123 PR-C commit 2: "Send test alert" dashboard form
	// (issue #1233). Sibling of the /enable exclusion above —
	// the dashboard's per-card test button posts to
	// /dashboard/apps/{slug}/alert-presets/{name}/test, but the
	// SDK does not model session-cookie-only dashboard auth.
	// Programmatic test-fire goes through the JSON sibling at
	// POST /v1/apps/{slug}/alert-presets/{name}/test, exposed as
	// TestAlertPreset in the SDK method map below.
	"POST /dashboard/apps/{slug}/alert-presets/{name}/test": true,

	// ADR-127 PR-D: OTLP sidecar protocol — not a REST endpoint
	// consumed by the generated SDK. OTel SDKs speak OTLP/HTTP
	// directly; the SDK would never mint IngestOtlpSpans as a
	// typed wrapper. Mirror in cmd/apid/spec_compliance_test.go::
	// routeExclude; the two lists must move together.
	"POST /v1/otel/v1/traces": true,
}

// sdkMethodExclude lists methods on *Client that aren't a 1:1 wire
// of any single spec route. Helpers (ListDeploymentsAll) and
// response-shape getters (HTTPClient/BaseURL/Token) belong here so
// the gate doesn't false-positive on them.
var sdkMethodExclude = map[string]bool{
	"HTTPClient":              true,
	"BaseURL":                 true,
	"Token":                   true,
	"ListDeploymentsAll":      true, // cursor walker; not a route
	"ListAppErrorRequestsAll": true, // cursor walker; not a route (ADR-096 / PR-B)
	"DeployMultipart":         true, // open-ended reader-based upload; CLI's DeployTarball is the wired route
	"MintCliAuthCode":         true, // anonymous device-code mint; route excluded above
	"ExchangeCliAuthCode":     true, // anonymous device-code poll; route excluded above
	"GetStatusSLO":            true, // public status; route excluded above
	"Logout":                  true, // POST /logout is a browser-form post (excluded above); the SDK wraps the same handler as a convenience
	"GetAccountDPA":           true, // public markdown; route excluded above (also reachable from /security)
	"GetMyOrg":                true, // GET /v1/orgs/me was added in PR #722 before the OpenAPI spec tracked it; track both in lockstep on the next spec pass
}

// methodRouteMap pins the routes whose natural SDK verb doesn't
// match the standard <Verb><Resource> derivation. Adding a new
// route? Add a row here ONLY if the auto-derivation picks the
// wrong method name; otherwise leave it auto-derived.
//
// Key = "<METHOD> <path>"; value = SDK method name.
var methodRouteMap = map[string]string{
	"DELETE /v1/keys/{id}":                        "DeleteKey",
	"POST /v1/keys/{id}/rotate":                   "RotateKey",
	"PATCH /v1/account/keys/grace_window_days":    "SetGraceWindow",
	"GET /v1/account/keys/grace_window_days":      "GetGraceWindow",
	"DELETE /v1/domains/{domain}":                 "DeleteDomain",
	"DELETE /v1/crons/{id}":                       "DeleteCron",
	"DELETE /v1/apps/{slug}":                      "DeleteApp",
	"DELETE /v1/apps/{slug}/secrets/{key}":        "UnsetSecret",
	"PUT /v1/apps/{slug}/secrets/{key}":           "SetSecret",
	"POST /v1/apps/{slug}/secrets/{key}/rotate":   "RotateSecret",
	"PATCH /v1/apps/{slug}":                       "UpdateApp",
	"POST /v1/apps/{slug}/rename":                 "RenameApp",
	"GET /v1/apps/{slug}":                         "GetApp",
	"GET /v1/apps/{slug}/instances":               "ListInstances",
	"POST /v1/apps/{slug}/park":                   "Park",
	"POST /v1/apps/{slug}/wake":                   "Wake",
	"POST /v1/apps/{slug}/rollback":               "Rollback",
	"POST /v1/apps/{slug}/rollouts/recover":       "RecoverRollout",
	"POST /v1/apps/{slug}/deployments":            "Deploy",
	"POST /v1/apps/{slug}/deployments/dev-source": "DeployDevSource",
	"POST /v1/apps/{slug}/deployments/source-ref": "DeployFromSourceRef", // issue #739 / DEPLOY-PROV-4 / ADR-092; headless CI deploy
	"POST /v1/apps/{slug}/diff":                   "Diff",                // PR-1 of deploy-diff cluster; CI gate input
	// Issue #961 / Mega-C PR-1 / leaf 3 — preview-destroy route.
	// Auto-derivation would produce "PostPreviewSlugDestroy" (the
	// Swagger-style verb+resource concat), but the SDK convention
	// uses ResourceVerb — pin to DestroyPreview so the wire +
	// SDK verb surface stays uniform with Rollback/Park/Wake.
	"POST /v1/preview/{slug}/destroy": "DestroyPreview",
	"GET /v1/account/export":          "ExportAccount",
	"DELETE /v1/account":              "DeleteAccount",
	"PATCH /v1/account/plan":          "ChangePlan",
	"GET /v1/account":                 "Whoami",
	"POST /v1/account/restore":        "RestoreAccount",
	"POST /v1/account/overage-cap":    "RaiseOverageCap", // issue #561 spend cap
	// Issue #679 / PR-B / ADR-082 — per-account additive budget on
	// top of the plan's apps.egress_allowlist cap. The auto-derivation
	// would concat "Account" + "Egress_allowlist_extra" (the literal
	// underscore survives title-case), so the explicit map drops the
	// underscore and aligns with the SDK's Get*EgressAllowlistExtra
	// method names.
	"GET /v1/account/egress_allowlist_extra":                  "GetEgressAllowlistExtra",
	"PATCH /v1/account/egress_allowlist_extra":                "SetEgressAllowlistExtra",
	"GET /v1/apps/{slug}/logs":                                "StreamAppLogs",
	"GET /v1/deployments/{id}/logs":                           "StreamDeploymentLogs",
	"GET /v1/deployments/{id}/scan":                           "GetDeploymentScan",              // issue #464 / ADR-055; per-deploy grype CVE drill-down
	"GET /v1/deployments/{id}/secret-scan":                    "GetDeploymentSecretScan",        // PR-A / ADR-101; per-deploy image-layer secret-scan audit row
	"GET /v1/deployments/{id}/stages":                         "GetDeploymentStages",            // ADR-117 follow-on; post-stream closed-stage summary for `gregale deploys show <id>`
	"GET /v1/deployments/{id}/audit":                          "ListDeploymentAudit",            // issue #976 / ADR-122 SAFE-RELEASES-E.2 + production-leveling Stream A; per-deployment audit timeline drill-down
	"POST /v1/deployments/{id}/canary/advance":                "AdvanceCanary",                  // issue #976 / ADR-122; APID-owned atomic canary CAS + traffic + audit
	"POST /v1/deployments/{id}/retry":                         "RetryDeploymentFromStage",       // ADR-117 §Production-ready follow-on C2; per-stage retry
	"GET /v1/deployments/{id}/url":                            "GetDeploymentURL",               // issue #976 / ADR-122 SAFE-RELEASES-C.2; per-deployment preview URL (deploy-N.slug.gregale.dev)
	"GET /v1/apps/{slug}/deployments/{deployment}/openapi":    "GetAppsDeploymentOpenAPIDoc",    // issue #975 item #1 / ADR-122 — captured OpenAPI doc per deployment
	"PATCH /v1/apps/{slug}/deployments/{deployment}/openapi":  "PatchAppsDeploymentOpenAPIDoc",  // manual upload; same store as cold-boot capture
	"DELETE /v1/apps/{slug}/deployments/{deployment}/openapi": "DeleteAppsDeploymentOpenAPIDoc", // wipe the captured doc; re-captures on next cold boot
	"GET /v1/apps/{slug}/env-diff":                            "GetAppEnvDiff",                  // ADR-117 PR-C: env vars + secrets × scopes matrix; matches operationId `getAppEnvDiff` (auto-derivation would produce `GetAppsSlugEnv-diff` because of the literal hyphen in the path segment — the explicit map drops the slug placeholder + the hyphen for Go SDK hygiene, mirroring the `GetAppMetrics` / `GetAppSLO` / `GetAppDataUpstream` precedent above)
	"GET /v1/apps/{slug}/openapi":                             "GetAppOpenAPI",                  // issue #975 item #2 / ADR-126 — imported or auto-generated OpenAPI doc per app
	"POST /v1/apps/{slug}/openapi":                            "ImportAppOpenAPI",               // manual upload (item #2 D2/D6); persists via UpsertAppOpenAPIDoc
	"DELETE /v1/apps/{slug}/openapi":                          "DeleteAppOpenAPI",               // idempotent wipe of the imported doc (item #2 D5 emits pg_notify)
	"POST /v1/apps/{slug}/openapi/dry-run":                    "DryRunAppOpenAPI",               // read-only edge-rule suggestions (item #2 D3)
	"GET /v1/deployments/{id}":                                "GetDeployment",
	"PATCH /v1/deployments/{id}":                              "PatchDeployment", // ADR-072 / issue #557 closure; min_instances override
	"DELETE /v1/deployments/{id}":                             "ClearDeployment", // ADR-124 PR-A; soft-delete (status untouched)
	"GET /v1/deployments":                                     "ListDeployments",
	"POST /v1/deployments/{id}/reorder":                       "ReorderDeployment",        // ADR-124 PR-A; priority bump on pending deploy
	"POST /v1/apps/{slug}/deployments/{id}/cancel":            "CancelDeployment",         // ADR-124 PR-A; status flip + cascade
	"POST /v1/apps/{slug}/deployments/clear-obsolete":         "ClearObsoleteDeployments", // ADR-124 PR-A; bulk soft-delete terminal rows
	"GET /v1/apps":                                            "ListApps",
	"POST /v1/apps":                                           "CreateApp",
	"GET /status/slo.json":                                    "GetStatusSLO",
	"PATCH /v1/crons/{id}":                                    "UpdateCron",
	"POST /v1/crons":                                          "CreateCron",
	"GET /v1/crons":                                           "ListCrons",
	"GET /v1/crons/{id}/runs":                                 "ListCronRuns",       // issue #791 — per-cron execution history
	"POST /v1/crons/{id}/run":                                 "FireCron",           // issue #791 — manual fire-now (PR-C)
	"GET /v1/cron-fire-now-requests/{request_id}":             "GetFireCronRequest", // issue #791 PR-D — poll fire-now terminal state (IDOR-safe byte-identical-404)
	"GET /v1/crons/{id}":                                      "GetCron",            // issue #791 PR-E / ADR-090 closure — backs `gregale crons info <id>`
	// Issue #1184 Workstream A — run-to-completion jobs. Same
	// resource-noun convention as crons + alerts + edge-rules:
	// auto-derivation produces verb+placeholder concatenation
	// ("PostJobsNameRuns" — Swagger-style); the SDK names
	// methods after the resource noun (Job / JobRun / JobTask).
	// Matches the `gregale jobs <list|add|info|update|rm|run|
	// runs|cancel|tasks|logs>` CLI surface at
	// cmd/gregale/commands_jobs.go.
	"GET /v1/jobs":                                   "ListJobs",
	"POST /v1/jobs":                                  "CreateJob",
	"GET /v1/jobs/{name}":                            "GetJob",
	"PATCH /v1/jobs/{name}":                          "UpdateJob",
	"DELETE /v1/jobs/{name}":                         "DeleteJob",
	"POST /v1/jobs/{name}/runs":                      "CreateJobRun",
	"GET /v1/jobs/{name}/runs":                       "ListJobRuns",
	"GET /v1/jobs/{name}/runs/{id}":                  "GetJobRun",
	"POST /v1/jobs/{name}/runs/{id}/cancel":          "CancelJobRun",
	"GET /v1/jobs/{name}/runs/{id}/tasks":            "ListJobRunTasks",
	"GET /v1/jobs/{name}/runs/{id}/tasks/{idx}/logs": "GetJobTaskLogs",
	"GET /v1/usage/summary":                          "UsageSummary",
	"GET /v1/usage":                                  "GetUsage",
	"GET /v1/usage/daily":                            "UsageDaily",
	"GET /v1/usage/storage":                          "StorageUsage",
	"GET /v1/invoices":                               "ListInvoices",
	"POST /v1/invocations/{id}/replay":               "ReplayInvocation", // issue #315 — re-issue a failed/dead_letter invocation
	"GET /v1/apps/{slug}/secrets":                    "ListSecrets",
	"GET /v1/domains":                                "ListDomains",
	"POST /v1/domains":                               "CreateDomain",

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
	// ADR-123 PR-D — operator pane for one rule's recent
	// alert_deliveries rows. ?include_test=true toggles the IsTest
	// discriminator; the SDK method reflects the boolean
	// explicitly so callers don't have to hand-craft URL params.
	"GET /v1/apps/{slug}/alerts/{id}/deliveries": "ListAlertRuleDeliveries",

	// Issue #1233 / ADR-123 — alert-preset catalog. Auto-derivation
	// would produce Swagger-style names ("GetAlert-presets",
	// "PostAppsSlugAlert-presetsNameEnable") because the path uses
	// a literal hyphen; the SDK names methods after the resource
	// noun (AlertPreset) — same convention as edge-rules and
	// throttle-suggestions above.
	"GET /v1/alert-presets":                            "ListAlertPresets",
	"POST /v1/apps/{slug}/alert-presets/{name}/enable": "EnableAlertPreset",
	// Issue #1233 / ADR-123 PR-C commit 2 — test-fire an
	// instantiated alert_preset rule against the customer's
	// webhook URL. Same hyphenated-path rationale as the /enable
	// sibling: auto-derivation would produce Swagger-style
	// "PostAppsSlugAlert-presetsNameTest" instead of a noun-based
	// method name.
	"POST /v1/apps/{slug}/alert-presets/{name}/test": "TestAlertPreset",

	// Issue #975 item #4 / ADR-129 — CORS presets. Same hyphen
	// pattern as alert-presets and edge-rules: auto-derivation
	// produces Swagger-style "GetCors-presets" because the path
	// uses a literal hyphen; the SDK names methods after the
	// resource noun (CorsPreset) — same convention as the
	// three precedents above.
	"GET /v1/cors-presets":         "ListCorsPresets",
	"POST /v1/cors-presets":        "CreateCorsPreset",
	"GET /v1/cors-presets/{id}":    "GetCorsPreset",
	"PATCH /v1/cors-presets/{id}":  "UpdateCorsPreset",
	"DELETE /v1/cors-presets/{id}": "DeleteCorsPreset",

	// ADR-089 (planned) — edge rules. The auto-derivation would
	// produce Swagger-style names ("GetAppsSlugEdge-rules",
	// "GetEdge-rules", "PostAppsSlugEdge-rules") because the path
	// uses a literal hyphen; the SDK names methods after the
	// resource noun (EdgeRule) — same convention as crons and
	// alerts above.
	"GET /v1/edge-rules":              "ListEdgeRules",
	"GET /v1/apps/{slug}/edge-rules":  "ListEdgeRulesForApp",
	"POST /v1/apps/{slug}/edge-rules": "CreateEdgeRule",
	"GET /v1/edge-rules/{id}":         "GetEdgeRule",
	"PATCH /v1/edge-rules/{id}":       "UpdateEdgeRule",
	"DELETE /v1/edge-rules/{id}":      "DeleteEdgeRule",
	// ADR-091 D20.5 amendment / issue #881 — per-route throttle
	// recommender. Auto-derivation would produce
	// "GetAppsSlugThrottle-suggestions" (literal hyphen) due to the
	// path's webhook-model naming; the SDK verb is the noun
	// "ThrottleSuggestions" so the explicit map drops the hyphen.
	"GET /v1/apps/{slug}/throttle-suggestions": "GetAppThrottleSuggestions",

	// Issue #476 / ADR-076 — outbound webhook subscriptions. Same
	// pattern as alerts: the SDK names the methods after the resource
	// noun (AppWebhook / AppWebhookDelivery) rather than the path
	// placeholder concatenation, and the rotate-secret + retry
	// routes need explicit pinning to drop the literal hyphen that
	// the auto-derivation would preserve.
	"GET /v1/apps/{slug}/webhooks":                              "ListAppWebhooks",
	"POST /v1/apps/{slug}/webhooks":                             "CreateAppWebhook",
	"GET /v1/apps/{slug}/webhooks/{id}":                         "GetAppWebhook",
	"PATCH /v1/apps/{slug}/webhooks/{id}":                       "UpdateAppWebhook",
	"DELETE /v1/apps/{slug}/webhooks/{id}":                      "DeleteAppWebhook",
	"POST /v1/apps/{slug}/webhooks/{id}/rotate-secret":          "RotateAppWebhookSecret",
	"GET /v1/apps/{slug}/webhooks/{id}/deliveries":              "ListAppWebhookDeliveries",
	"POST /v1/apps/{slug}/webhooks/{id}/deliveries/{did}/retry": "RetryAppWebhookDelivery",

	// ADR-098 §9.A — connection-aware data upstreams (PR-B hand-off).
	// The auto-derivation would produce Swagger-style names
	// ("GetAppsSlugUpstreams", "GetAppsSlugUpstreamsId", etc.) because
	// the path uses the literal "upstreams" segment; the SDK names the
	// methods after the resource noun (DataUpstream) — same convention
	// as alerts/edge-rules/webhooks above. The PUT route is the
	// upsert/create verb (the spec writes a single row per (kind, host,
	// port) tuple, with the response carrying the persisted id).
	"GET /v1/apps/{slug}/upstreams":                               "ListAppDataUpstreams",
	"GET /v1/apps/{slug}/upstreams/{id}":                          "GetAppDataUpstream",
	"PUT /v1/apps/{slug}/upstreams":                               "CreateAppDataUpstream",
	"DELETE /v1/apps/{slug}/upstreams/{id}":                       "DeleteAppDataUpstream",
	"GET /v1/keys":                                                "ListKeys",
	"GET /v1/apps/{slug}/buckets":                                 "ListObjectBuckets",
	"GET /v1/account/object-storage-usage":                        "GetObjectStorageUsage",
	"POST /v1/admin/object-storage/usage-reports":                 "RecordObjectStorageUsage",
	"POST /v1/apps/{slug}/buckets":                                "CreateObjectBucket",
	"DELETE /v1/apps/{slug}/buckets/{bucket}":                     "DeleteObjectBucket",
	"GET /v1/apps/{slug}/buckets/{bucket}/access-grants":          "ListObjectBucketAccessGrants",
	"PUT /v1/apps/{slug}/buckets/{bucket}/access-grants/{key}":    "SetObjectBucketAccessGrant",
	"DELETE /v1/apps/{slug}/buckets/{bucket}/access-grants/{key}": "DeleteObjectBucketAccessGrant",
	"GET /v1/apps/{slug}/buckets/{bucket}/objects":                "ListBucketObjects",
	"DELETE /v1/apps/{slug}/buckets/{bucket}/objects":             "DeleteBucketObject",
	"POST /v1/apps/{slug}/buckets/{bucket}/signed-url":            "SignBucketObject",
	"POST /v1/keys": "CreateKey",
	// Move 2 routes — the auto-derivation produces names with literal
	// hyphens (e.g. "DeleteDelayed-tasksId") because the spec path uses
	// the k8s-style hyphen; the explicit map below drops the hyphen and
	// conforms to the SDK's flat resource naming.
	"POST /v1/apps/{slug}/invoke":                         "InvokeApp",
	"POST /v1/apps/{slug}/invoke/async":                   "InvokeAppAsync",
	"POST /v1/apps/{slug}/queues/send":                    "QueueSend",
	"POST /v1/apps/{slug}/queues/receive":                 "QueueReceive",
	"POST /v1/apps/{slug}/queues/{id}/ack":                "AckQueueRow",
	"GET /v1/apps/{slug}/queues/state":                    "QueueState",
	"GET /v1/apps/{slug}/queues/peek":                     "QueuePeek",
	"GET /v1/apps/{slug}/queues/dead_letter":              "QueueDeadLetter",
	"POST /v1/apps/{slug}/queues/dead_letter/{id}/replay": "QueueDeadLetterReplay",
	"POST /v1/apps/{slug}/delayed-tasks":                  "CreateDelayedTask",
	"GET /v1/delayed-tasks/{id}":                          "GetDelayedTask",
	"DELETE /v1/delayed-tasks/{id}":                       "CancelDelayedTask",
	"GET /v1/invocations":                                 "ListInvocations",
	"GET /v1/invocations/{id}":                            "GetInvocation",
	// Issue #279 — operator credits. The auto-derivation produces
	// "PostAdminAccountsIdCredits" which reads as a Swagger-style
	// artifact; the SDK verb is "issue" (the operator's mental
	// model) so the explicit map takes precedence.
	"POST /v1/admin/accounts/{id}/credits": "IssueAccountCredit",
	// Operator refunds use a local invoice binding and the provider's
	// idempotency key; the SDK follows the operator mental model rather than
	// the generated PostAdminAccountsIdRefunds name.
	"POST /v1/admin/accounts/{id}/refunds": "RefundAccount",
	// PR-D / ADR-012 §7 amendment — per-tenant webhook secret
	// rotation. Auto-derivation produces
	// "PostAdminGithub-webhook-secrets" (literal hyphen); the SDK
	// verb is "set" (operator's mental model) so the explicit map
	// takes precedence.
	"POST /v1/admin/github-webhook-secrets": "SetGithubWebhookSecret",
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

	// Issue #755 / PR-6 — audit_log dashboard surface. Reads the
	// FK-free audit_log table (migrations/00163_audit_log.sql);
	// distinct from /v1/audit-events which reads the live events
	// table. Two routes by audience (customer-scoped / operator).
	"GET /v1/audit-log":     "ListAuditLog",
	"GET /v1/audit-log/all": "ListAuditLogAll",

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

	// ADR-124 follow-up #3 — persistent --exclude history. The
	// auto-derivation would produce "DeleteProjectsSlugExclusionsSlug2"
	// (Swagger-style with both path placeholders preserved); the SDK
	// names it after the resource noun (DeploymentScopeExclusion)
	// so the explicit map drops both slug placeholders — same
	// convention as the edge-rules / app-webhooks / alert-rules
	// clusters above. The delete path is the operator-undo escape
	// hatch when a persisted slug is blocking deploys.
	"DELETE /v1/projects/{slug}/exclusions/{slug2}": "DeleteDeploymentScopeExclusion",

	// Issue #311 — gregale signup split from login (PR #786). The
	// programmatic-auth surface is JSON-only and orthogonal to the
	// cookie-auth /login /signup dashboard routes (which are
	// pin-mapped to PasswordLogin / PasswordSignup further up).
	// The auto-derivation would produce PostAuthSignup / PostAuthLogin
	// (matches the SDK) but a literal-hyphen
	// "PostAuthSignupMagic-link" for the magic-link route — Go method
	// names can't carry hyphens, so the SDK normalises it to
	// PostAuthSignupMagicLink. Pin all three so the gate stays the
	// SDK's source of truth on verb choice (same convention as the
	// audit-events / registry-credentials / paddle-catalog patterns
	// above).
	"POST /v1/auth/signup":            "PostAuthSignup",
	"POST /v1/auth/login":             "PostAuthLogin",
	"POST /v1/auth/signup/magic-link": "PostAuthSignupMagicLink",

	// PR-P3 — operator billing surface (issue #279 / ADR-049 + ADR-050).
	// The auto-derivation produces names with literal hyphens
	// (e.g. "GetAdminBilling-paddle-catalog") because the spec paths
	// carry the k8s-style hyphen; the SDK verbs follow the operationId
	// (ListPaddleCatalog, ResetPaddleCatalog, SyncPaddleCatalog,
	// ReconcileAccount) so the explicit map drops the path-separator
	// noise and keeps the SDK surface cohesive with the CLI
	// (`faas billing status|price-catalog ...`).
	"GET /v1/admin/billing-paddle-catalog":           "ListPaddleCatalog",
	"DELETE /v1/admin/billing-paddle-catalog":        "ResetPaddleCatalog",
	"POST /v1/admin/billing-paddle-catalog/sync":     "SyncPaddleCatalog",
	"POST /v1/admin/billing-reconcile/{id}":          "ReconcileAccount",
	"GET /v1/admin/billing-paddle-overage/preflight": "GetBillingPaddleOveragePreflight",

	// ADR-089 PR-C — background re-seal progress. The auto-derivation
	// would produce "GetAdminSecretsRekey-progress" (the dash survives
	// titleCase), which is awkward in Go. Pin the name to match the
	// Go SDK; the method lives in pkg/api/client.go next to the
	// PR-P3 admin billing surface.
	"GET /v1/admin/secrets/rekey-progress": "GetRekeyProgress",

	// Issue #273 / ADR-042 — per-app metrics. The auto-derivation
	// would produce GetAppsSlugMetrics (Swagger-style); the SDK
	// names it GetAppMetrics to match the existing per-app methods
	// (GetApp, ListApps) — drop the slug placeholder from the verb.
	"GET /v1/apps/{slug}/metrics": "GetAppMetrics",

	// Per-app observability backend PR series (PR #1097). The
	// wake-timeline path uses a literal hyphen (matching the
	// dashboard route); the auto-derivation would produce
	// GetAppsSlugWake-timeline (Swagger-style with the hyphen
	// preserved). The usage path auto-derives to
	// GetAppsSlugUsage — drop the slug placeholder from the verb
	// to match the sibling per-app family (GetAppMetrics,
	// GetAppSLO, GetAppRoutes) and use the DTO type name
	// (AppUsageSummary) for the noun.
	"GET /v1/apps/{slug}/wake-timeline":        "GetAppWakeTimeline",
	"GET /v1/apps/{slug}/usage":                "GetAppUsageSummary",
	"GET /v1/apps/{slug}/analytics":            "GetAppRequestAnalytics",
	"GET /v1/apps/{slug}/analytics/timeseries": "GetAppRequestAnalyticsTimeseries",

	// ADR-127 / PR-A — production debugger data plane. The
	// auto-derivation would produce GetAppsSlugDebugRequests
	// (Swagger-style); the SDK names it ListAppDebugRequests to
	// match the operationId on the spec side and the per-resource
	// list family (ListAlertRules, ListEdgeRules, ListAppWebhooks)
	// — drop the slug placeholder from the verb.
	"GET /v1/apps/{slug}/debug/requests":          "ListAppDebugRequests",
	"GET /v1/apps/{slug}/debug/requests/{req_id}": "GetAppDebugRequest",

	// ADR-127 / PR-B — production debugger consumer surface.
	// Same rationale as the PR-A request list: drop the slug from
	// the verb so the per-resource list family stays consistent
	// (ListAppDebugRegressions reads as a sibling of
	// ListAppDebugRequests).
	"GET /v1/apps/{slug}/debug/regressions":               "ListAppDebugRegressions",
	"POST /v1/apps/{slug}/debug/compare":                  "CompareAppDebugDeployments",
	"POST /v1/apps/{slug}/debug/requests/{req_id}/replay": "ReplayAppDebugRequest",

	// Issue #393 — account-scoped list endpoints. Distinct from the
	// per-app counterparts (ListInstances / ListSecrets / GetAppMetrics)
	// which take a slug; the aggregate route has no slug, so the SDK
	// uses Get<Resource> to flag the account-scoped contract (one call
	// replaces N per-app calls — see ADR-045).
	"GET /v1/instances":    "GetInstances",
	"GET /v1/secrets":      "GetSecrets",
	"GET /v1/apps/metrics": "GetAppsMetrics",

	// Issue #696 / ADR-082 — customer-facing SLO surface.
	// Closed-set windowed panel (1h | 24h | 7d) — distinct from
	// the /metrics entry above which is the 5m dashboard panel.
	// Per-app pattern mirrors GetAppMetrics; account-scoped
	// mirrors GetAccountUsage (the usage account-scoped family).
	"GET /v1/apps/{slug}/slo": "GetAppSLO",
	"GET /v1/account/slo":     "GetAccountSLO",

	// ADR-093 — per-route observability inside an app. The
	// auto-derivation would produce GetAppsSlugRoutes
	// (Swagger-style); the SDK names it GetAppRoutes to match the
	// sibling per-app family (GetAppMetrics, GetAppSLO, GetApp,
	// ListApps) — drop the slug placeholder from the verb.
	"GET /v1/apps/{slug}/routes": "GetAppRoutes",

	// ADR-102 D6 — per-app streaming classification probe. The
	// auto-derivation would produce GetAppsSlugStreaming-cap
	// (Swagger-style with literal hyphen); the SDK names it
	// GetAppStreamingStatus to match the sibling per-app family
	// (GetAppRoutes, GetAppMetrics, GetAppSLO) — drop the slug
	// placeholder from the verb and use the SDK type name
	// (AppStreamingStatus) for the noun.
	"GET /v1/apps/{slug}/streaming-cap": "GetAppStreamingStatus",

	// ADR-096 / PR-B — customer-facing automatic error grouping.
	// SDK names are pinned to the per-app family (GetAppErrorsSummary,
	// ListAppErrorRequests, GetAppErrorSample) — auto-derivation would
	// produce GetAppsSlugErrorsSummary / GetAppsSlugErrorsFingerprintFirst
	// (Swagger-style) which breaks the navigability match.
	"GET /v1/apps/{slug}/errors/summary":             "GetAppErrorsSummary",
	"GET /v1/apps/{slug}/errors/{fingerprint}":       "ListAppErrorRequests",
	"GET /v1/apps/{slug}/errors/{fingerprint}/first": "GetAppErrorSample",

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
	// becomes "PostAuthLogout". The SDK named this method after the
	// account-scoped noun (Post*Account*) to mirror the
	// Get/Post/Patch/Delete pattern used elsewhere; pin it.
	//
	// GET /v1/auth/sessions, DELETE /v1/auth/sessions/{id}, and
	// POST /v1/auth/sessions/revoke_all are intentionally not
	// exposed: those handlers read sessionFrom(r), which is
	// cookie-only (pkg/auth/middleware/context.go:141), so they
	// reject bearer-key callers with 401. Same reasoning drops
	// GET /v1/auth/capabilities (sessionAuth at server.go:1085).
	"POST /v1/auth/logout": "PostAccountLogout",

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

	// Issue #190 / IAM-6 / ADR-061 PR 5 — /v1/orgs/{slug}/... customer
	// surface. The auto-derivation would produce names with the
	// `{slug}` / `{user_id}` / `{token}` placeholders preserved as
	// title-cased path segments ("GetOrgsSlug", "DeleteOrgsSlug"),
	// matching the failure log we hit on the first PR 5 push. The
	// explicit map below drops the path-segment noise and conforms
	// to the SDK's flat resource-verb naming — same convention as
	// the crons / alerts / keys clusters above. Account-scoped
	// list + create skip the X-Active-Org hint, path-scoped routes
	// require it (apid loadOrg middleware stamps the membership).
	"GET /v1/orgs":                               "ListOrgs",
	"POST /v1/orgs":                              "CreateOrg",
	"GET /v1/orgs/{slug}":                        "GetOrg",
	"PATCH /v1/orgs/{slug}":                      "PatchOrg",
	"DELETE /v1/orgs/{slug}":                     "DeleteOrg",
	"GET /v1/orgs/{slug}/members":                "ListOrgMembers",
	"POST /v1/orgs/{slug}/members":               "InviteOrgMember",
	"PATCH /v1/orgs/{slug}/members/{user_id}":    "ChangeOrgMemberRole",
	"DELETE /v1/orgs/{slug}/members/{user_id}":   "RemoveOrgMember",
	"POST /v1/orgs/{slug}/transfer_ownership":    "TransferOrgOwnership",
	"GET /v1/invitations/{token}":                "PeekInvitation",
	"POST /v1/invitations/{token}/accept":        "AcceptInvitation",
	"DELETE /v1/orgs/{slug}/invitations/{token}": "RevokeInvitation",
	// PR-8 §2 — cursor-paginated list of every invitation minted on
	// the org (pending/consumed/revoked/expired). Auto-derivation
	// yields "GetOrgsSlugInvitations" which reads as a singleton
	// fetch; the explicit map entry pins the List<Collection> verb
	// the §2 surface uses. (ListOrgInvitationsAll is the cursor
	// walker — it has no spec route, so sdk-coverage logs it as
	// "warn" and the gate stays green.)
	"GET /v1/orgs/{slug}/invitations": "ListOrgInvitations",
	"GET /v1/orgs/{slug}/seat_usage":  "GetOrgSeatUsage",

	// PR 6 (issue #190 / IAM-6 / ADR-061) — org-scoped API key
	// surface. The auto-derivation would produce
	// "GetOrgsSlugKeys" / "PostOrgsSlugKeys" etc., which read as
	// Swagger-style artefacts and don't match the SDK's
	// Resource-noun convention (ListOrgAPIKeys / CreateOrgAPIKey).
	// Five routes share the keys sub-namespace on an arbitrary
	// org slug; pin them so the gate stays the SDK's source of
	// truth on verb choice, matching the trusted_signers /
	// registry-credentials pattern above.
	"GET /v1/orgs/{slug}/keys":              "ListOrgAPIKeys",
	"POST /v1/orgs/{slug}/keys":             "CreateOrgAPIKey",
	"GET /v1/orgs/{slug}/keys/{id}":         "GetOrgAPIKey",
	"DELETE /v1/orgs/{slug}/keys/{id}":      "RevokeOrgAPIKey",
	"POST /v1/orgs/{slug}/keys/{id}/rotate": "RotateOrgAPIKey",

	// Issue #757 / ADR-100 — unified Trigger primitive. Pin every
	// trigger route; auto-derivation reads either "Triggers" or
	// "TriggersId<Segment>" depending on whether the path carries
	// hyphens, and we want the SDK verb surface to be uniform.
	// The two colon-suffixed routes use the colon-stripped Go name
	// (Go identifiers can't carry `:` so the SDK normalises the
	// path component to the next segment word).
	"POST /v1/triggers":                          "PostTriggers",
	"GET /v1/triggers":                           "GetTriggers",
	"POST /v1/triggers:batch_create":             "PostTriggersBatchCreate",
	"GET /v1/triggers/{id}":                      "GetTriggersId",
	"PATCH /v1/triggers/{id}":                    "PatchTriggersId",
	"DELETE /v1/triggers/{id}":                   "DeleteTriggersId",
	"POST /v1/triggers/{id}/pause":               "PostTriggersIdPause",
	"POST /v1/triggers/{id}/resume":              "PostTriggersIdResume",
	"GET /v1/triggers/{id}/records":              "GetTriggersIdRecords",
	"POST /v1/triggers/{id}/records/{rid}/retry": "PostTriggersIdRecordsRidRetry",
	"POST /v1/triggers/{id}/records/{rid}/drop":  "PostTriggersIdRecordsRidDrop",
	"GET /v1/triggers/{id}/dlq":                  "GetTriggersIdDlq",
	"GET /v1/triggers/{id}/metrics":              "GetTriggersIdMetrics",
	// Internal — schedd posts the batch envelope to the gateway.
	// The SDK surface is optional; the handler is registered on
	// the gateway synth plane.
	"POST /v1/invocations:dispatch_batch": "PostInvocationsDispatchBatch",
	// Issue #879 / ADR-100 PR-C — tenant surfaces. The auto-derivation
	// produces names with literal hyphens (the path carries the
	// "tenant-surfaces" segment); the SDK verbs follow the operationId
	// (ListTenantSurfaces, CreateTenantSurface, etc.) so the
	// explicit map drops the path-separator noise and keeps the SDK
	// surface cohesive with the CLI (`gregale tenant-surfaces ...`).
	"GET /v1/apps/{slug}/tenant-surfaces":                              "ListTenantSurfaces",
	"POST /v1/apps/{slug}/tenant-surfaces":                             "CreateTenantSurface",
	"GET /v1/apps/{slug}/tenant-surfaces/{id}":                         "GetTenantSurface",
	"DELETE /v1/apps/{slug}/tenant-surfaces/{id}":                      "DeleteTenantSurface",
	"POST /v1/apps/{slug}/tenant-surfaces/{id}/hostnames":              "AddTenantHostname",
	"DELETE /v1/apps/{slug}/tenant-surfaces/{id}/hostnames/{hostname}": "RemoveTenantHostname",

	// ADR-119 — per-app static egress IP. Same shape as tenant-surfaces:
	// the auto-derivation strips the {slug} placeholder but leaves the
	// hyphen in "static-egress-ip", producing
	// "GetAppsSlugStatic-egress-ip" which doesn't match the SDK verbs
	// (GetAppStaticEgressIP, SetAppStaticEgressIP, ClearAppStaticEgressIP).
	// The explicit map drops the path-separator noise and matches the
	// spec operationId for each verb.
	"GET /v1/apps/{slug}/static-egress-ip":    "GetAppStaticEgressIP",
	"PUT /v1/apps/{slug}/static-egress-ip":    "SetAppStaticEgressIP",
	"DELETE /v1/apps/{slug}/static-egress-ip": "ClearAppStaticEgressIP",

	// Issue #961 / Mega-A PR-A — zero-config deploy + domains surface.
	// The auto-derivation produces names with literal hyphens for the
	// source-tarball route ("PostAppsSlugDeploymentsSource-tarball")
	// because the path segment carries a hyphen; the SDK verb is
	// "DeployFromSourceTarball" to mirror DeployFromSourceRef (PR #937).
	// GET /v1/domains/{domain} auto-derives to "GetDomainsDomain" (Swagger
	// placeholder noise); the SDK verb drops it, matching the per-app
	// family (GetApp, GetDeployment, GetUsage).
	"POST /v1/apps/{slug}/deployments/source-tarball": "DeployFromSourceTarball",
	"GET /v1/domains/{domain}":                        "GetDomain",
	"POST /v1/domains/{domain}/verify":                "VerifyDomain",
	"GET /v1/domains/{domain}/doctor":                 "DomainDoctor",
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
